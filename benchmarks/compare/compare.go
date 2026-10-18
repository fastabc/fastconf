// Package compare contains the complete adapters used by the comparison suite.
package compare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing/fstest"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/contracts"
	"github.com/go-viper/mapstructure/v2"
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/knadh/koanf/v2"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

type Entry struct {
	Name  string `json:"name" yaml:"name" mapstructure:"name"`
	Value int    `json:"value" yaml:"value" mapstructure:"value"`
}
type Config struct {
	Counter int     `json:"counter" yaml:"counter" mapstructure:"counter"`
	Mirror  int     `json:"mirror" yaml:"mirror" mapstructure:"mirror"`
	Entries []Entry `json:"entries" yaml:"entries" mapstructure:"entries"`
	Padding string  `json:"padding" yaml:"padding" mapstructure:"padding"`
}

func Validate(c *Config) error {
	if c.Counter < 1 || c.Mirror != c.Counter {
		return fmt.Errorf("invalid counter/mirror")
	}
	return nil
}

// Fixture produces exactly size bytes, one YAML layer, size/128 list entries.
// Revisions 1 and 2 have identical byte lengths; 0 fails the shared validator.
func Fixture(size, revision int) []byte {
	c := Config{Counter: revision, Mirror: revision}
	for i := 0; i < size/128; i++ {
		c.Entries = append(c.Entries, Entry{fmt.Sprintf("item-%06d", i), i})
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		panic(err)
	}
	c.Padding = strings.Repeat("x", size-len(data)+2) // Replace YAML's quoted empty string.
	data, err = yaml.Marshal(c)
	if err != nil || len(data) != size {
		panic(fmt.Sprintf("fixture size=%d want=%d err=%v", len(data), size, err))
	}
	return data
}

type document struct{ data []byte }

func (*document) Name() string { return "memory-yaml" }
func (d *document) Load(context.Context) (contracts.Snapshot, error) {
	var tree map[string]any
	err := yaml.Unmarshal(d.data, &tree)
	return contracts.Snapshot{Map: tree}, err
}
func (d *document) ReadBytes() ([]byte, error) { return d.data, nil }
func (d *document) Read() (map[string]any, error) {
	s, err := d.Load(context.Background())
	return s.Map, err
}

// Decode uses each library's public decoding API. Koanf accepts a map provider;
// document.Read performs the same yaml.v3 parse as the FastConf provider.
func Decode(name string, data []byte) (*Config, error) {
	c := new(Config)
	var err error
	switch name {
	case "FastConf":
		var s *fastconf.State[Config]
		s, err = fastconf.Load[Config](context.Background(), fastconf.WithFS(fstest.MapFS{}),
			fastconf.WithProvider(&document{data}), fastconf.WithUnknownFields(fastconf.UnknownError), fastconf.WithValidate(Validate))
		if err == nil {
			return s.Value(), nil
		}
	case "Viper":
		v := viper.New()
		v.SetConfigType("yaml")
		if err = v.ReadConfig(bytes.NewReader(data)); err == nil {
			err = v.UnmarshalExact(c)
		}
	case "Koanf":
		k := koanf.New(".")
		if err = k.Load(&document{data}, nil); err == nil {
			err = k.UnmarshalWithConf("", c, koanf.UnmarshalConf{Tag: "mapstructure", DecoderConfig: &mapstructure.DecoderConfig{ErrorUnused: true}})
		}
	case "Cleanenv":
		err = cleanenv.ParseYAML(bytes.NewReader(data), c)
	default:
		return nil, fmt.Errorf("unknown adapter %s", name)
	}
	if err == nil {
		err = Validate(c)
	}
	return c, err
}

// Adapter serializes Reload at the caller; Get returns an immutable snapshot.
// Viper and Koanf adapters own their hash deduplication and atomic publication.
type Adapter struct {
	name    string
	source  *document
	manager *fastconf.Manager[Config]
	value   atomic.Pointer[Config]
	hash    [32]byte
}

func Open(name string, initial []byte) (*Adapter, error) {
	a := &Adapter{name: name}
	if name == "FastConf" {
		a.source = &document{initial}
		m, err := fastconf.New[Config](context.Background(), fastconf.WithFS(fstest.MapFS{}),
			fastconf.WithProvider(a.source), fastconf.WithUnknownFields(fastconf.UnknownError), fastconf.WithValidate(Validate))
		a.manager = m
		return a, err
	}
	err := a.Reload(initial)
	return a, err
}
func (a *Adapter) Reload(data []byte) error {
	if a.manager != nil {
		a.source.data = data
		return a.manager.Reload(context.Background())
	}
	c, err := Decode(a.name, data)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(encoded)
	if hash != a.hash {
		a.hash = hash
		a.value.Store(c)
	}
	return nil
}
func (a *Adapter) Get() *Config {
	if a.manager != nil {
		return a.manager.Get()
	}
	return a.value.Load()
}
func (a *Adapter) Close() error {
	if a.manager != nil {
		return a.manager.Close()
	}
	return nil
}

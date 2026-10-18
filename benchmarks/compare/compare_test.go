package compare

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fastabc/fastconf"
	"github.com/fastabc/fastconf/providers/env"
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/kelseyhightower/envconfig"
)

var sizes = []int{4 << 10, 64 << 10, 1 << 20}
var dynamicNames = []string{"FastConf", "Viper", "Koanf"}
var sink int

func TestAdapters(t *testing.T) {
	for _, size := range sizes {
		data := Fixture(size, 1)
		want, err := Decode("FastConf", data)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("size=%d entries=%d sha256=%x", size, len(want.Entries), sha256.Sum256(data))
		for _, name := range []string{"FastConf", "Viper", "Koanf", "Cleanenv"} {
			got, err := Decode(name, data)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("%s size=%d decode: %v", name, size, err)
			}
		}
		for _, name := range dynamicNames {
			a, err := Open(name, data)
			if err != nil {
				t.Fatal(err)
			}
			before := a.Get()
			if err := a.Reload(data); err != nil || a.Get() != before {
				t.Fatalf("%s noop: %v", name, err)
			}
			if err := a.Reload(Fixture(size, 0)); err == nil || a.Get() != before {
				t.Fatalf("%s invalid", name)
			}
			if err := a.Reload(Fixture(size, 2)); err != nil || a.Get().Counter != 2 || a.Get() == before {
				t.Fatalf("%s update: %v", name, err)
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestConcurrentAdapters(t *testing.T) {
	for _, name := range dynamicNames {
		t.Run(name, func(t *testing.T) {
			first, second := Fixture(4096, 1), Fixture(4096, 2)
			a, err := Open(name, first)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = a.Close() }()
			started, stop, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			go func() {
				defer close(done)
				close(started)
				for {
					select {
					case <-stop:
						return
					default:
						c := a.Get()
						if c.Counter != c.Mirror {
							t.Error("mixed snapshot")
							return
						}
					}
				}
			}()
			defer func() { close(stop); <-done }()
			<-started
			for i := 0; i < 20; i++ {
				data := first
				if i%2 == 0 {
					data = second
				}
				if err := a.Reload(data); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCompare(b *testing.B) {
	for _, size := range sizes {
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			data, next, invalid := Fixture(size, 1), Fixture(size, 2), Fixture(size, 0)
			for _, name := range []string{"FastConf", "Viper", "Koanf", "Cleanenv"} {
				b.Run("Startup/"+name, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(size))
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						c, err := Decode(name, data)
						if err != nil {
							b.Fatal(err)
						}
						sink = c.Counter
					}
				})
			}
			for _, name := range dynamicNames {
				for _, operation := range []string{"ValidReload", "NoopReload", "InvalidReload"} {
					b.Run(operation+"/"+name, func(b *testing.B) {
						a, err := Open(name, data)
						if err != nil {
							b.Fatal(err)
						}
						defer func() { _ = a.Close() }()
						b.ReportAllocs()
						b.SetBytes(int64(size))
						b.ResetTimer()
						for i := 0; i < b.N; i++ {
							input := data
							if operation == "ValidReload" && i%2 == 0 {
								input = next
							}
							if operation == "InvalidReload" {
								input = invalid
							}
							err := a.Reload(input)
							if (err != nil) != (operation == "InvalidReload") {
								b.Fatal(err)
							}
						}
						b.StopTimer()
						want := 1
						if operation == "ValidReload" && b.N%2 == 1 {
							want = 2
						}
						if a.Get().Counter != want {
							b.Fatal("wrong published value")
						}
					})
				}
			}
		})
	}
}

func BenchmarkHotRead(b *testing.B) {
	data := Fixture(4<<10, 1)
	c, err := Decode("FastConf", data)
	if err != nil {
		b.Fatal(err)
	}
	var pointer atomic.Pointer[Config]
	pointer.Store(c)
	b.Run("Struct", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			sink = c.Counter
		}
	})
	b.Run("Atomic", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			sink = pointer.Load().Counter
		}
	})
	for _, name := range dynamicNames {
		a, err := Open(name, data)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				sink = a.Get().Counter
			}
		})
		if err := a.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConcurrentRead(b *testing.B) {
	for _, name := range dynamicNames {
		b.Run(name, func(b *testing.B) {
			data, next := Fixture(4<<10, 1), Fixture(4<<10, 2)
			a, err := Open(name, data)
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = a.Close() }()
			stop, done := make(chan struct{}), make(chan error, 1)
			go func() {
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for i := 0; ; i++ {
					select {
					case <-stop:
						done <- nil
						return
					case <-ticker.C:
					}
					input := data
					if i%2 == 0 {
						input = next
					}
					if err := a.Reload(input); err != nil {
						done <- err
						return
					}
				}
			}()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				local := 0
				for pb.Next() {
					c := a.Get()
					if c.Counter != c.Mirror {
						b.Error("mixed snapshot")
					}
					local = c.Counter
				}
				runtime.KeepAlive(local)
			})
			b.StopTimer()
			close(stop)
			if err := <-done; err != nil {
				b.Fatal(err)
			}
		})
	}
}

func BenchmarkFileStartup(b *testing.B) {
	for _, size := range sizes {
		path := filepath.Join(b.TempDir(), "app.yaml")
		if err := os.WriteFile(path, Fixture(size, 1), 0o600); err != nil {
			b.Fatal(err)
		}
		for _, name := range []string{"FastConf", "Viper", "Koanf", "Cleanenv"} {
			b.Run(fmt.Sprintf("%dKiB/%s", size>>10, name), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					data, err := os.ReadFile(path)
					if err != nil {
						b.Fatal(err)
					}
					c, err := Decode(name, data)
					if err != nil {
						b.Fatal(err)
					}
					sink = c.Counter
				}
			})
		}
	}
}

type envConfig struct {
	Port    int  `json:"port" env:"FC_COMPARE_PORT" envconfig:"PORT"`
	Enabled bool `json:"enabled" env:"FC_COMPARE_ENABLED" envconfig:"ENABLED"`
}

func readEnv(name string) (*envConfig, error) {
	c := new(envConfig)
	var err error
	switch name {
	case "FastConf":
		var s *fastconf.State[envConfig]
		s, err = fastconf.Load[envConfig](context.Background(), fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(env.New("FC_COMPARE_")), fastconf.WithUnknownFields(fastconf.UnknownError))
		if err == nil {
			c = s.Value()
		}
	case "Cleanenv":
		err = cleanenv.ReadEnv(c)
	case "Envconfig":
		err = envconfig.Process("FC_COMPARE", c)
	}
	if err == nil && (c.Port < 1 || !c.Enabled) {
		err = fmt.Errorf("invalid env config")
	}
	return c, err
}
func BenchmarkEnvStartup(b *testing.B) {
	b.Setenv("FC_COMPARE_PORT", "8080")
	b.Setenv("FC_COMPARE_ENABLED", "true")
	for _, name := range []string{"FastConf", "Cleanenv", "Envconfig"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c, err := readEnv(name)
				if err != nil {
					b.Fatal(err)
				}
				sink = c.Port
			}
		})
	}
}
func TestEnvAdapters(t *testing.T) {
	t.Setenv("FC_COMPARE_PORT", "8080")
	t.Setenv("FC_COMPARE_ENABLED", "true")
	for _, name := range []string{"FastConf", "Cleanenv", "Envconfig"} {
		c, err := readEnv(name)
		if err != nil || *c != (envConfig{8080, true}) {
			t.Fatalf("%s: %v %v", name, c, err)
		}
	}
}

func BenchmarkFastConfDiagnostics(b *testing.B) {
	for _, size := range sizes {
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			doc := &document{Fixture(size, 1)}
			m, err := fastconf.New[Config](context.Background(), fastconf.WithFS(fstest.MapFS{}), fastconf.WithProvider(doc), fastconf.WithValidate(Validate), fastconf.WithHistory(8))
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = m.Close() }()
			b.Run("MapWarm", func(b *testing.B) {
				_ = m.Snapshot().Map()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = m.Snapshot().Map()
				}
			})
			data, next := Fixture(size, 1), Fixture(size, 2)
			b.Run("ReloadHistory8MapCold", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if i%2 == 0 {
						doc.data = next
					} else {
						doc.data = data
					}
					if err := m.Reload(context.Background()); err != nil {
						b.Fatal(err)
					}
					_ = m.Snapshot().Map()
				}
			})
		})
	}
}

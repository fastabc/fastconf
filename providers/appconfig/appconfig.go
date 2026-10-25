// Package appconfig loads deployed AWS AppConfig documents through the Data API.
// It is load-only; applications schedule Reload and own their SDK clients.
package appconfig

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/appconfigdata"
	"github.com/fastabc/fastconf/codec"
	"github.com/fastabc/fastconf/confmap"
	"github.com/fastabc/fastconf/contracts"
	"github.com/fastabc/fastconf/internal/providerutil"
)

// API is implemented by *appconfigdata.Client.
type API interface {
	StartConfigurationSession(context.Context, *sdk.StartConfigurationSessionInput, ...func(*sdk.Options)) (*sdk.StartConfigurationSessionOutput, error)
	GetLatestConfiguration(context.Context, *sdk.GetLatestConfigurationInput, ...func(*sdk.Options)) (*sdk.GetLatestConfigurationOutput, error)
}

// Config selects a deployed configuration. Codec may be omitted to use the
// response Content-Type. MinimumPollInterval defaults to 60s (whole seconds,
// 15s..24h). The service may require a longer interval.
type Config struct {
	Application string
	Environment string
	Profile     string

	Codec               string
	Priority            int // Defaults to contracts.PriorityKV.
	MinimumPollInterval time.Duration
}

// Provider serializes the session's one-use tokens and caches decoded content.
type Provider struct {
	cfg      Config
	client   API
	decoder  contracts.Decoder
	maxBytes int64
	loadGate chan struct{} // Serializes loads while allowing waiting callers to cancel.
	token    string
	nextPoll time.Time
	data     map[string]any
	revision string
	loaded   bool
	lastErr  error
	now      func() time.Time
}

// Option configures a provider before registration.
type Option func(*Provider)

// WithMaxBodyBytes bounds decoded input (default 4 MiB). The SDK buffers the
// response before this check. Non-positive values restore the default.
func WithMaxBodyBytes(n int64) Option {
	return func(p *Provider) {
		if n <= 0 {
			n = contracts.DefaultMaxBodyBytes
		}
		p.maxBytes = n
	}
}

// New borrows an application-configured SDK client without changing it.
func New(cfg Config, client API, opts ...Option) (*Provider, error) {
	if client == nil || cfg.Application == "" || cfg.Environment == "" || cfg.Profile == "" {
		return nil, errors.New("fastconf/appconfig: client, application, environment and profile are required")
	}

	if cfg.MinimumPollInterval == 0 {
		cfg.MinimumPollInterval = time.Minute
	}
	if cfg.MinimumPollInterval < 15*time.Second || cfg.MinimumPollInterval > 24*time.Hour || cfg.MinimumPollInterval%time.Second != 0 {
		return nil, errors.New("fastconf/appconfig: minimum poll interval must be whole seconds in 15s..24h")
	}
	if cfg.Priority == 0 {
		cfg.Priority = contracts.PriorityKV
	}

	p := &Provider{
		cfg:      cfg,
		client:   client,
		maxBytes: contracts.DefaultMaxBodyBytes,
		now:      time.Now,
		loadGate: make(chan struct{}, 1),
	}
	if cfg.Codec != "" {
		var err error
		p.decoder, err = codec.For(cfg.Codec)
		if err != nil {
			return nil, err
		}
	}

	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// Name implements contracts.Provider.
func (p *Provider) Name() string {
	return "appconfig:" + p.cfg.Application + "/" + p.cfg.Environment + "/" + p.cfg.Profile
}

// Describe implements contracts.Describer.
func (p *Provider) Describe() contracts.ProviderInfo {
	return contracts.ProviderInfo{Priority: p.cfg.Priority}
}

// Load respects the service's poll interval. Before the next poll it returns
// cached content (or the last error), so notifications cannot force early calls.
// Continue periodic reloads even when SNS is configured.
// Tokens are advanced before decoding, and rejected content remains an error
// across empty responses until a later valid document arrives.
// Concurrent loads are serialized; waiting for a load honors ctx cancellation.
// https://docs.aws.amazon.com/appconfig/2019-10-09/APIReference/API_appconfigdata_GetLatestConfiguration.html
func (p *Provider) Load(ctx context.Context) (contracts.Snapshot, error) {
	select {
	case p.loadGate <- struct{}{}:
		defer func() { <-p.loadGate }()
	case <-ctx.Done():
		return contracts.Snapshot{}, ctx.Err()
	}

	// Cancellation may race with acquisition of an available gate.
	if err := ctx.Err(); err != nil {
		return contracts.Snapshot{}, err
	}

	if p.now().Before(p.nextPoll) {
		return p.snapshot()
	}

	if p.token == "" {
		out, err := p.client.StartConfigurationSession(ctx, &sdk.StartConfigurationSessionInput{
			ApplicationIdentifier:                aws.String(p.cfg.Application),
			EnvironmentIdentifier:                aws.String(p.cfg.Environment),
			ConfigurationProfileIdentifier:       aws.String(p.cfg.Profile),
			RequiredMinimumPollIntervalInSeconds: aws.Int32(int32(p.cfg.MinimumPollInterval / time.Second)),
		})
		if err != nil {
			p.nextPoll = p.now().Add(p.cfg.MinimumPollInterval)
			return p.fail("start session", err)
		}
		p.token = aws.ToString(out.InitialConfigurationToken)
		if p.token == "" {
			return p.fail("start session", errors.New("missing configuration token"))
		}
	}

	token := p.token
	p.token = "" // Never replay a token after an uncertain network outcome.
	out, err := p.client.GetLatestConfiguration(ctx, &sdk.GetLatestConfigurationInput{ConfigurationToken: &token})
	p.nextPoll = p.now().Add(p.cfg.MinimumPollInterval)
	if err != nil {
		return p.fail("get configuration", err)
	}
	p.token = aws.ToString(out.NextPollConfigurationToken)
	interval := max(p.cfg.MinimumPollInterval, time.Duration(out.NextPollIntervalInSeconds)*time.Second)
	p.nextPoll = p.now().Add(interval)
	if len(out.Configuration) == 0 {
		return p.snapshot()
	}
	if int64(len(out.Configuration)) > p.maxBytes {
		return p.fail("decode", contracts.ErrTooLarge)
	}

	decoder := p.decoder
	if decoder == nil {
		var ok bool
		decoder, ok = codec.ByContentType(aws.ToString(out.ContentType))
		if !ok {
			return p.fail("decode", codec.ErrUnknownCodec)
		}
	}
	data, err := decoder.Decode(out.Configuration)
	if err != nil {
		return p.fail("decode", err)
	}

	p.data, p.revision = data, fmt.Sprintf("%x", sha256.Sum256(out.Configuration))
	p.loaded, p.lastErr = true, nil
	return p.snapshot()
}

func (p *Provider) snapshot() (contracts.Snapshot, error) {
	if p.lastErr != nil {
		return contracts.Snapshot{}, p.lastErr
	}
	if !p.loaded {
		return contracts.Snapshot{}, errors.New("fastconf/appconfig: no configuration received")
	}
	return contracts.Snapshot{Map: confmap.DeepClone(p.data), Revision: p.revision}, nil
}

func (p *Provider) fail(operation string, err error) (contracts.Snapshot, error) {
	p.lastErr = providerutil.SafeError("fastconf/appconfig: "+operation+" failed", err)
	return contracts.Snapshot{}, p.lastErr
}

var _ contracts.Provider = (*Provider)(nil)
var _ contracts.Describer = (*Provider)(nil)

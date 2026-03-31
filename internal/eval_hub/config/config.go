package config

import "time"

const (
	// SidecarTerminationFilePath is used for Kubernetes termination messages.
	SidecarTerminationFilePath = "/data/termination-log"
)

type Config struct {
	Service    *ServiceConfig    `mapstructure:"service"`
	Database   *map[string]any   `mapstructure:"database"`
	MLFlow     *MLFlowConfig     `mapstructure:"mlflow,omitempty"`
	ModelProxy *ModelProxyConfig `mapstructure:"model_proxy,omitempty"`
	OTEL       *OTELConfig       `mapstructure:"otel,omitempty"`
	Prometheus *PrometheusConfig `mapstructure:"prometheus,omitempty"`
	Sidecar    *SidecarConfig    `mapstructure:"sidecar,omitempty"`
}

// ModelProxyConfig is runtime configuration for the sidecar model reverse proxy (from sidecar_config.json model block).
// JSON tags match SidecarModelConfig so the shape is obvious if this struct is ever marshaled or documented beside the wire format.
type ModelProxyConfig struct {
	URL                string        `json:"url,omitempty"`
	AuthAPIKeyPath     string        `json:"auth_api_key_path,omitempty"` // empty: use pod service account token at default path
	AuthCACertPath     string        `json:"auth_ca_cert_path,omitempty"` // optional; TLS trust for upstream model
	InsecureSkipVerify bool          `json:"insecure_skip_verify,omitempty"`
	HTTPTimeout        time.Duration `json:"http_timeout,omitempty"`
	TokenCacheTimeout  time.Duration `json:"token_cache_timeout,omitempty"`
}

func (c *Config) IsOTELEnabled() bool {
	return (c != nil) && (c.OTEL != nil) && c.OTEL.Enabled
}

func (c *Config) IsOTELStorageScansEnabled() bool {
	return c.IsOTELEnabled() && !c.OTEL.DisableDatabaseOTELScans
}

func (c *Config) IsPrometheusEnabled() bool {
	return (c != nil) && (c.Prometheus != nil) && c.Prometheus.Enabled
}

func (c *Config) IsAuthenticationEnabled() bool {
	return (c != nil) && (c.Service != nil) && !c.Service.DisableAuth && !c.Service.LocalMode
}

"""Application configuration."""

from functools import lru_cache
from typing import Any

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    """Application settings."""

    model_config = SettingsConfigDict(
        env_file=".env", env_file_encoding="utf-8", case_sensitive=False, extra="allow"
    )

    # Application settings
    app_name: str = Field(default="Eval Hub", description="Application name")
    version: str = Field(default="0.1.0", description="Application version")
    debug: bool = Field(default=False, description="Debug mode")
    log_level: str = Field(default="INFO", description="Logging level")

    # API settings
    api_host: str = Field(default="0.0.0.0", description="API host")
    api_port: int = Field(default=8000, description="API port")
    api_prefix: str = Field(default="/api/v1", description="API path prefix")
    docs_url: str = Field(default="/docs", description="Swagger UI URL")
    redoc_url: str = Field(default="/redoc", description="ReDoc URL")
    openapi_url: str = Field(default="/openapi.json", description="OpenAPI schema URL")

    # Security
    secret_key: str = Field(
        default="your-secret-key-change-in-production", description="Secret key for JWT"
    )
    access_token_expire_minutes: int = Field(
        default=30, description="Access token expiration"
    )

    # Database settings
    database_url: str | None = Field(
        default=None, description="Database connection URL"
    )

    # MLFlow settings
    mlflow_tracking_uri: str = Field(
        default="http://localhost:5000", description="MLFlow tracking server URI"
    )
    mlflow_experiment_prefix: str = Field(
        default="eval-hub", description="MLFlow experiment name prefix"
    )
    mlflow_artifact_location: str | None = Field(
        default=None, description="MLFlow artifact storage location"
    )

    # S3/Object Storage (for MLFlow artifacts)
    s3_endpoint_url: str | None = Field(default=None, description="S3 endpoint URL")
    s3_access_key_id: str | None = Field(default=None, description="S3 access key")
    s3_secret_access_key: str | None = Field(default=None, description="S3 secret key")
    s3_bucket_name: str | None = Field(default=None, description="S3 bucket name")

    # Evaluation settings
    max_concurrent_evaluations: int = Field(
        default=10, description="Maximum concurrent evaluations"
    )
    default_timeout_minutes: int = Field(
        default=60, description="Default evaluation timeout"
    )
    max_retry_attempts: int = Field(default=3, description="Maximum retry attempts")

    # Backend configurations
    backend_configs: dict[str, dict[str, Any]] = Field(
        default_factory=lambda: {
            "lm-evaluation-harness": {
                "image": "eval-harness:latest",
                "resources": {"cpu": "2", "memory": "4Gi"},
                "timeout": 3600,
            },
"lm_evaluation_harness-backend": {
  "deploy_crs": False,
  "model": "local-completions",
"model_args": "{\"model\":\"qwen2.5-1.5b-instruct.gguf\",\"base_url\":\"http://localhost:8001/v1/completions\",\"tokenizer\":\"Qwen/Qwen2.5-1.5B\"}"
},
            "guidellm": {
                "image": "guidellm:latest",
                "resources": {"cpu": "1", "memory": "2Gi"},
                "timeout": 1800,
            },
        },
        description="Backend-specific configurations",
    )

    # Risk category benchmark mappings
    risk_category_benchmarks: dict[str, dict[str, Any]] = Field(
        default_factory=lambda: {
            "low": {
                "benchmarks": ["hellaswag", "arc_easy"],
                "num_fewshot": 5,
                "limit": 100,
            },
            "medium": {
                "benchmarks": ["hellaswag", "arc_easy", "arc_challenge", "winogrande"],
                "num_fewshot": 5,
                "limit": 500,
            },
            "high": {
                "benchmarks": [
                    "hellaswag",
                    "arc_easy",
                    "arc_challenge",
                    "winogrande",
                    "mmlu",
                ],
                "num_fewshot": 5,
                "limit": 1000,
            },
            "critical": {
                "benchmarks": [
                    "hellaswag",
                    "arc_easy",
                    "arc_challenge",
                    "winogrande",
                    "mmlu",
                    "gsm8k",
                ],
                "num_fewshot": 5,
                "limit": None,
            },
        },
        description="Risk category to benchmark mappings",
    )

    # Monitoring settings
    enable_metrics: bool = Field(default=True, description="Enable Prometheus metrics")
    metrics_port: int = Field(default=8001, description="Metrics server port")

    # OpenShift/Kubernetes settings
    namespace: str = Field(default="default", description="Kubernetes namespace")
    service_account: str | None = Field(
        default=None, description="Service account name"
    )

    # Callback settings
    callback_timeout_seconds: int = Field(
        default=30, description="Callback request timeout"
    )
    callback_retry_attempts: int = Field(
        default=3, description="Callback retry attempts"
    )

    @property
    def database_url_sync(self) -> str:
        """Get synchronous database URL."""
        if self.database_url:
            return self.database_url.replace("postgresql+asyncpg://", "postgresql://")
        return "sqlite:///./eval_hub.db"

    @property
    def database_url_async(self) -> str:
        """Get asynchronous database URL."""
        if self.database_url:
            return self.database_url
        return "sqlite+aiosqlite:///./eval_hub.db"


@lru_cache
def get_settings() -> Settings:
    """Get cached application settings."""
    return Settings()

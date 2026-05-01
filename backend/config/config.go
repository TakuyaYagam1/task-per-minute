package config

import (
	"fmt"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	HTTP      HTTP      `env-prefix:"HTTP_"`
	DB        DB        `env-prefix:"DB_"`
	Redis     Redis     `env-prefix:"REDIS_"`
	SeaweedFS SeaweedFS `env-prefix:"SEAWEEDFS_"`
	JWT       JWT       `env-prefix:"JWT_"`
	Admin     Admin     `env-prefix:"ADMIN_"`
}

type HTTP struct {
	Host            string        `env:"HOST"             env-default:"0.0.0.0"`
	Port            int           `env:"PORT"             env-default:"8080"`
	ReadTimeout     time.Duration `env:"READ_TIMEOUT"     env-default:"15s"`
	WriteTimeout    time.Duration `env:"WRITE_TIMEOUT"    env-default:"15s"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" env-default:"30s"`
}

type DB struct {
	DSN      string `env:"DSN"       env-required:"true"`
	MaxConns int32  `env:"MAX_CONNS" env-default:"20"`
}

type Redis struct {
	Addr     string `env:"ADDR"     env-default:"localhost:6379"`
	Password string `env:"PASSWORD"`
	DB       int    `env:"DB"       env-default:"0"`
}

type SeaweedFS struct {
	Endpoint  string `env:"ENDPOINT"   env-required:"true"`
	AccessKey string `env:"ACCESS_KEY" env-required:"true"`
	SecretKey string `env:"SECRET_KEY" env-required:"true"`
	Bucket    string `env:"BUCKET"     env-default:"task-per-minute"`
	Secure    bool   `env:"SECURE"     env-default:"false"`
}

type JWT struct {
	Secret     string        `env:"SECRET"      env-required:"true"`
	AccessTTL  time.Duration `env:"ACCESS_TTL"  env-default:"15m"`
	RefreshTTL time.Duration `env:"REFRESH_TTL" env-default:"168h"`
}

type Admin struct {
	Password string `env:"PASSWORD" env-required:"true"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		return nil, fmt.Errorf("config - Load - cleanenv.ReadEnv: %w", err)
	}
	return &cfg, nil
}

func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}

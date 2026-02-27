package config

import (
	"log"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Environment string         `mapstructure:"ENVIRONMENT"`
	Server      ServerConfig   `mapstructure:",squash"`
	Database    DatabaseConfig `mapstructure:",squash"`
	Redis       RedisConfig    `mapstructure:",squash"`
	MQTT        MQTTConfig     `mapstructure:",squash"`
	JWT         JWTConfig      `mapstructure:",squash"`
	MinIO       MinIOConfig    `mapstructure:",squash"`
	Admin       AdminConfig    `mapstructure:",squash"`
}

type ServerConfig struct {
	Port         string `mapstructure:"API_PORT"`
	ReadTimeout  int    `mapstructure:"READ_TIMEOUT"`
	WriteTimeout int    `mapstructure:"WRITE_TIMEOUT"`
}

type DatabaseConfig struct {
	Host     string `mapstructure:"DB_HOST"`
	Port     string `mapstructure:"DB_PORT"`
	User     string `mapstructure:"DB_USER"`
	Password string `mapstructure:"DB_PASSWORD"`
	Name     string `mapstructure:"DB_NAME"`
	SSLMode  string `mapstructure:"DB_SSLMODE"`
}

type RedisConfig struct {
	Host     string `mapstructure:"REDIS_HOST"`
	Port     string `mapstructure:"REDIS_PORT"`
	Password string `mapstructure:"REDIS_PASSWORD"`
	DB       int    `mapstructure:"REDIS_DB"`
}

type MQTTConfig struct {
	Broker       string `mapstructure:"MQTT_BROKER"`
	DeviceBroker string `mapstructure:"MQTT_DEVICE_BROKER"`
	Port         string `mapstructure:"MQTT_PORT"`
	Username     string `mapstructure:"MQTT_USERNAME"`
	Password     string `mapstructure:"MQTT_PASSWORD"`
	ClientID     string `mapstructure:"MQTT_CLIENT_ID"`
}

type JWTConfig struct {
	Secret          string `mapstructure:"JWT_SECRET"`
	ExpiryHours     int    `mapstructure:"JWT_EXPIRY_HOURS"`
	RefreshDays     int    `mapstructure:"JWT_REFRESH_DAYS"`
	DeviceExpiryDay int    `mapstructure:"JWT_DEVICE_EXPIRY_DAYS"`
}

type MinIOConfig struct {
	Endpoint         string `mapstructure:"MINIO_ENDPOINT"`
	ExternalEndpoint string `mapstructure:"MINIO_EXTERNAL_ENDPOINT"`
	AccessKey        string `mapstructure:"MINIO_ACCESS_KEY"`
	SecretKey        string `mapstructure:"MINIO_SECRET_KEY"`
	Bucket           string `mapstructure:"MINIO_BUCKET"`
	UseSSL           bool   `mapstructure:"MINIO_USE_SSL"`
}

type AdminConfig struct {
	Email          string `mapstructure:"ADMIN_EMAIL"`
	Password       string `mapstructure:"ADMIN_PASSWORD"`
	OrganizationID string `mapstructure:"ADMIN_ORG_ID"`
}

func Load() (*Config, error) {
	viper.SetConfigFile(".env")
	viper.SetConfigType("env")

	// Set defaults
	viper.SetDefault("ENVIRONMENT", "development")
	viper.SetDefault("API_PORT", "8080")
	viper.SetDefault("READ_TIMEOUT", 10)
	viper.SetDefault("WRITE_TIMEOUT", 10)

	viper.SetDefault("DB_HOST", "s8sgs4ooo4ssokggkssks4wk")
	viper.SetDefault("DB_PORT", "5432")
	viper.SetDefault("DB_USER", "postgres")
	viper.SetDefault("DB_PASSWORD", "17ORR8ceETHDPS0IVgAjHjLuW5PfGzZOqbtRCtFeifE76RKjTx1EktAHjTMQvtTY")
	viper.SetDefault("DB_NAME", "postgres")
	viper.SetDefault("DB_SSLMODE", "disable")

	viper.SetDefault("REDIS_HOST", "109.199.100.87")
	viper.SetDefault("REDIS_PORT", "6379")
	viper.SetDefault("REDIS_PASSWORD", "mdm_redis_pass_2026")
	viper.SetDefault("REDIS_DB", 0)

	viper.SetDefault("MQTT_BROKER", "109.199.100.87")
	viper.SetDefault("MQTT_DEVICE_BROKER", "109.199.100.87")
	viper.SetDefault("MQTT_PORT", "1883")
	viper.SetDefault("MQTT_USERNAME", "mdm_server")
	viper.SetDefault("MQTT_PASSWORD", "mdm_mqtt_pass")
	viper.SetDefault("MQTT_CLIENT_ID", "mdm_server_01")

	viper.SetDefault("JWT_SECRET", "your_super_secret_jwt_key_change_in_production")
	viper.SetDefault("JWT_EXPIRY_HOURS", 24)
	viper.SetDefault("JWT_REFRESH_DAYS", 7)
	viper.SetDefault("JWT_DEVICE_EXPIRY_DAYS", 365)

	viper.SetDefault("MINIO_ENDPOINT", "109.199.100.87:9000")
	viper.SetDefault("MINIO_EXTERNAL_ENDPOINT", "109.199.100.87:9000")
	viper.SetDefault("MINIO_ACCESS_KEY", "mdm_minio_admin")
	viper.SetDefault("MINIO_SECRET_KEY", "mdm_minio_pass_2026")
	viper.SetDefault("MINIO_BUCKET", "mdm-files")
	viper.SetDefault("MINIO_USE_SSL", false)

	viper.SetDefault("ADMIN_EMAIL", "admin@mdm-system.com")
	viper.SetDefault("ADMIN_PASSWORD", "admin_secure_2026")
	viper.SetDefault("ADMIN_ORG_ID", "00000000-0000-0000-0000-000000000001")

	// Read from environment variables
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Try to read config file
	if err := viper.ReadInConfig(); err != nil {
		log.Printf("Config file not found, using defaults and environment variables: %v", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

func (c *DatabaseConfig) DSN() string {
	return "postgres://" + c.User + ":" + c.Password + "@" + c.Host + ":" + c.Port + "/" + c.Name + "?sslmode=" + c.SSLMode
}

// Copyright 2021 gorse Project Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"context"
	"crypto/md5"
	_ "embed"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/expr-lang/expr/parser"
	"github.com/go-playground/locales/en"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	en_translations "github.com/go-playground/validator/v10/translations/en"
	"github.com/go-viper/mapstructure/v2"
	"github.com/gorse-io/gorse/common/expression"
	"github.com/gorse-io/gorse/common/log"
	"github.com/gorse-io/gorse/common/util"
	"github.com/gorse-io/gorse/storage"
	"github.com/juju/errors"
	"github.com/samber/lo"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/zipkin"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.8.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

func init() {
	viper.SetOptions(viper.WithDecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		StringToFeedbackTypeHookFunc(),
	)))
}

// Config is the configuration for the engine.
type Config struct {
	Database  DatabaseConfig  `mapstructure:"database"`
	Master    MasterConfig    `mapstructure:"master"`
	Server    ServerConfig    `mapstructure:"server"`
	Recommend RecommendConfig `mapstructure:"recommend"`
	Tracing   TracingConfig   `mapstructure:"tracing"`
	OIDC      OIDCConfig      `mapstructure:"oidc"`
	OpenAI    OpenAIConfig    `mapstructure:"openai"`
	Blob      BlobConfig      `mapstructure:"blob"`
}

// DatabaseConfig is the configuration for the database.
type DatabaseConfig struct {
	DataStore        string      `mapstructure:"data_store" validate:"required,data_store"`   // database for data store
	CacheStore       string      `mapstructure:"cache_store" validate:"required,cache_store"` // database for cache store
	TablePrefix      string      `mapstructure:"table_prefix"`
	DataTablePrefix  string      `mapstructure:"data_table_prefix"`
	CacheTablePrefix string      `mapstructure:"cache_table_prefix"`
	MySQL            MySQLConfig `mapstructure:"mysql"`
	Postgres         SQLConfig   `mapstructure:"postgres"`
	Redis            RedisConfig `mapstructure:"redis"`
}

type MySQLConfig struct {
	IsolationLevel  string        `mapstructure:"isolation_level" validate:"oneof=READ-UNCOMMITTED READ-COMMITTED REPEATABLE-READ SERIALIZABLE"`
	MaxOpenConns    int           `mapstructure:"max_open_conns" validate:"gte=0"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns" validate:"gte=0"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime" validate:"gte=0"`
}

type SQLConfig struct {
	MaxOpenConns    int           `mapstructure:"max_open_conns" validate:"gte=0"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns" validate:"gte=0"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime" validate:"gte=0"`
}

type RedisConfig struct {
	MaxSearchResults int `mapstructure:"max_search_results" validate:"gt=0"`
}

func (db *DatabaseConfig) StorageOptions(path string) []storage.Option {
	if strings.HasPrefix(path, storage.MySQLPrefix) {
		return []storage.Option{
			storage.WithIsolationLevel(db.MySQL.IsolationLevel),
			storage.WithMaxOpenConns(db.MySQL.MaxOpenConns),
			storage.WithMaxIdleConns(db.MySQL.MaxIdleConns),
			storage.WithConnMaxLifetime(db.MySQL.ConnMaxLifetime),
		}
	}
	if strings.HasPrefix(path, storage.PostgresPrefix) || strings.HasPrefix(path, storage.PostgreSQLPrefix) {
		return []storage.Option{
			storage.WithMaxOpenConns(db.Postgres.MaxOpenConns),
			storage.WithMaxIdleConns(db.Postgres.MaxIdleConns),
			storage.WithConnMaxLifetime(db.Postgres.ConnMaxLifetime),
		}
	}
	if strings.HasPrefix(path, storage.RedisPrefix) || strings.HasPrefix(path, storage.RedissPrefix) ||
		strings.HasPrefix(path, storage.RedisClusterPrefix) || strings.HasPrefix(path, storage.RedissClusterPrefix) {
		return []storage.Option{
			storage.WithMaxSearchResults(db.Redis.MaxSearchResults),
		}
	}
	return nil
}

// MasterConfig is the configuration for the master.
type MasterConfig struct {
	Port              int           `mapstructure:"port" validate:"gte=0"`        // master port
	Host              string        `mapstructure:"host"`                         // master host
	SSLMode           bool          `mapstructure:"ssl_mode"`                     // enable SSL mode
	SSLCA             string        `mapstructure:"ssl_ca"`                       // SSL CA file
	SSLCert           string        `mapstructure:"ssl_cert"`                     // SSL certificate file
	SSLKey            string        `mapstructure:"ssl_key"`                      // SSL key file
	HttpPort          int           `mapstructure:"http_port" validate:"gte=0"`   // HTTP port
	HttpHost          string        `mapstructure:"http_host"`                    // HTTP host
	HttpCorsDomains   []string      `mapstructure:"http_cors_domains"`            // add allowed cors domains
	HttpCorsMethods   []string      `mapstructure:"http_cors_methods"`            // add allowed cors methods
	NumJobs           int           `mapstructure:"n_jobs" validate:"gt=0"`       // number of working jobs
	MetaTimeout       time.Duration `mapstructure:"meta_timeout" validate:"gt=0"` // cluster meta timeout (second)
	DashboardUserName string        `mapstructure:"dashboard_user_name"`          // dashboard user name
	DashboardPassword string        `mapstructure:"dashboard_password"`           // dashboard password
	DashboardRedacted bool          `mapstructure:"dashboard_redacted"`
	AdminAPIKey       string        `mapstructure:"admin_api_key"`
}

// ServerConfig is the configuration for the server.
type ServerConfig struct {
	APIKey         string        `mapstructure:"api_key"`                      // default number of returned items
	DefaultN       int           `mapstructure:"default_n" validate:"gt=0"`    // secret key for RESTful APIs (SSL required)
	ClockError     time.Duration `mapstructure:"clock_error" validate:"gte=0"` // clock error in the cluster in seconds
	AutoInsertUser bool          `mapstructure:"auto_insert_user"`             // insert new users while inserting feedback
	AutoInsertItem bool          `mapstructure:"auto_insert_item"`             // insert new items while inserting feedback
	CacheExpire    time.Duration `mapstructure:"cache_expire" validate:"gt=0"` // server-side cache expire time
}

// RecommendConfig is the configuration of recommendation setup.
type RecommendConfig struct {
	CacheSize       int                     `mapstructure:"cache_size" validate:"gt=0"`
	CacheExpire     time.Duration           `mapstructure:"cache_expire" validate:"gt=0"`
	ContextSize     int                     `mapstructure:"context_size" validate:"gt=0"`
	ActiveUserTTL   int                     `mapstructure:"active_user_ttl" validate:"gte=0"`
	DataSource      DataSourceConfig        `mapstructure:"data_source"`
	NonPersonalized []NonPersonalizedConfig `mapstructure:"non-personalized" validate:"dive"`
	ItemToItem      []ItemToItemConfig      `mapstructure:"item-to-item" validate:"dive"`
	UserToUser      []UserToUserConfig      `mapstructure:"user-to-user" validate:"dive"`
	Collaborative   CollaborativeConfig     `mapstructure:"collaborative"`
	External        []ExternalConfig        `mapstructure:"external" validate:"dive"`
	Replacement     ReplacementConfig       `mapstructure:"replacement"`
	Ranker          RankerConfig            `mapstructure:"ranker"`
	Fallback        FallbackConfig          `mapstructure:"fallback"`
	// Lifecycle-aware recall pools
	Lifecycle    LifecycleConfig    `mapstructure:"lifecycle"`
	RecallPools  []RecallPoolConfig `mapstructure:"recall_pools" validate:"dive"`
	SupplyDemand SupplyDemandConfig `mapstructure:"supply_demand"`
	Fatigue     FatigueConfig     `mapstructure:"fatigue"`
	Quality       QualityConfig       `mapstructure:"quality"`
	VIP           VIPConfig           `mapstructure:"vip"`
	Behavior      BehaviorConfig      `mapstructure:"behavior"`
	ItemStats     ItemStatsConfig     `mapstructure:"item_stats"`
	MMR           MMRConfig           `mapstructure:"mmr"`
	SuccessRate   SuccessRateConfig   `mapstructure:"success_rate"`
	SessionFatigue SessionFatigueConfig `mapstructure:"session_fatigue"`
	SocialGraph   SocialGraphConfig   `mapstructure:"social_graph"`
}

func (r *RecommendConfig) ListRecommenders() []string {
	recommenders := make([]string, 0)
	for _, rec := range r.NonPersonalized {
		recommenders = append(recommenders, rec.FullName())
	}
	for _, rec := range r.ItemToItem {
		recommenders = append(recommenders, rec.FullName())
	}
	for _, rec := range r.UserToUser {
		recommenders = append(recommenders, rec.FullName())
	}
	for _, rec := range r.External {
		recommenders = append(recommenders, rec.FullName())
	}
	recommenders = append(recommenders, r.Collaborative.FullName())
	recommenders = append(recommenders, "latest")
	return recommenders
}

func (r *RecommendConfig) Hash() string {
	recommenders := mapset.NewSet(r.Ranker.Recommenders...)
	if recommenders.IsEmpty() {
		recommenders.Append((r.ListRecommenders())...)
	}
	var digests []string
	for _, rec := range r.NonPersonalized {
		if recommenders.Contains(rec.FullName()) {
			digests = append(digests, rec.Hash())
		}
	}
	for _, rec := range r.ItemToItem {
		if recommenders.Contains(rec.FullName()) {
			digests = append(digests, rec.Hash(r))
		}
	}
	for _, rec := range r.UserToUser {
		if recommenders.Contains(rec.FullName()) {
			digests = append(digests, rec.Hash(r))
		}
	}
	for _, rec := range r.External {
		if recommenders.Contains(rec.FullName()) {
			digests = append(digests, rec.Hash())
		}
	}
	if recommenders.Contains(r.Collaborative.FullName()) {
		digests = append(digests, r.Collaborative.Hash(r))
	}
	if recommenders.Contains("latest") {
		digests = append(digests, "latest")
	}
	return util.MD5(digests...)
}

func StringToFeedbackTypeHookFunc() mapstructure.DecodeHookFunc {
	return func(
		f reflect.Type,
		t reflect.Type,
		data any,
	) (any, error) {
		if f.Kind() == reflect.String && t == reflect.TypeFor[expression.FeedbackTypeExpression]() {
			var expr expression.FeedbackTypeExpression
			if err := expr.FromString(data.(string)); err != nil {
				return nil, errors.Trace(err)
			}
			return expr, nil // only convert string to FeedbackType
		}
		return data, nil
	}
}

type DataSourceConfig struct {
	PositiveFeedbackTypes []expression.FeedbackTypeExpression `mapstructure:"positive_feedback_types"`                // positive feedback type
	NegativeFeedbackTypes []expression.FeedbackTypeExpression `mapstructure:"negative_feedback_types"`                // negative feedback type (highest priority)
	ReadFeedbackTypes     []expression.FeedbackTypeExpression `mapstructure:"read_feedback_types"`                    // feedback type for read event
	PositiveFeedbackTTL   uint                                `mapstructure:"positive_feedback_ttl" validate:"gte=0"` // time-to-live of positive feedbacks
	ItemTTL               uint                                `mapstructure:"item_ttl" validate:"gte=0"`              // item-to-live of items
}

type NonPersonalizedConfig struct {
	Name   string `mapstructure:"name" json:"name"`
	Score  string `mapstructure:"score" json:"score" validate:"required,item_expr"`
	Filter string `mapstructure:"filter" json:"filter" validate:"item_expr"`
}

func (config *NonPersonalizedConfig) FullName() string {
	return "non-personalized/" + config.Name
}

func (config *NonPersonalizedConfig) Hash() string {
	hash := md5.New()
	hash.Write([]byte(config.Name))
	hash.Write([]byte(config.Score))
	hash.Write([]byte(config.Filter))
	return hex.EncodeToString(hash.Sum(nil)[:])
}

type ItemToItemConfig struct {
	Name   string `mapstructure:"name" json:"name"`
	Type   string `mapstructure:"type" json:"type" validate:"oneof=embedding tags users chat auto"`
	Column string `mapstructure:"column" json:"column" validate:"item_expr"`
	Prompt string `mapstructure:"prompt" json:"prompt"`
}

func (config *ItemToItemConfig) FullName() string {
	return "item-to-item/" + config.Name
}

func (config *ItemToItemConfig) Hash(cfg *RecommendConfig) string {
	hash := md5.New()
	hash.Write([]byte(config.Name))
	hash.Write([]byte(config.Type))
	hash.Write([]byte(config.Column))
	if config.Type == "users" {
		for _, expr := range cfg.DataSource.PositiveFeedbackTypes {
			hash.Write([]byte(expr.String()))
		}
		for _, expr := range cfg.DataSource.NegativeFeedbackTypes {
			hash.Write([]byte(expr.String()))
		}
	}
	return hex.EncodeToString(hash.Sum(nil)[:])
}

type UserToUserConfig struct {
	Name   string `mapstructure:"name" json:"name"`
	Type   string `mapstructure:"type" json:"type" validate:"oneof=embedding tags items auto"`
	Column string `mapstructure:"column" json:"column" validate:"item_expr"`
}

func (config *UserToUserConfig) FullName() string {
	return "user-to-user/" + config.Name
}

func (config *UserToUserConfig) Hash(cfg *RecommendConfig) string {
	hash := md5.New()
	hash.Write([]byte(config.Name))
	hash.Write([]byte(config.Type))
	hash.Write([]byte(config.Column))
	if config.Type == "items" {
		for _, expr := range cfg.DataSource.PositiveFeedbackTypes {
			hash.Write([]byte(expr.String()))
		}
		for _, expr := range cfg.DataSource.NegativeFeedbackTypes {
			hash.Write([]byte(expr.String()))
		}
	}
	return hex.EncodeToString(hash.Sum(nil)[:])
}

type CollaborativeConfig struct {
	Type           string              `mapstructure:"type" validate:"oneof=none mf"`
	FitPeriod      time.Duration       `mapstructure:"fit_period" validate:"gt=0"`
	FitEpoch       int                 `mapstructure:"fit_epoch" validate:"gt=0"`
	OptimizePeriod time.Duration       `mapstructure:"optimize_period" validate:"gte=0"`
	OptimizeTrials int                 `mapstructure:"optimize_trials" validate:"gt=0"`
	EarlyStopping  EarlyStoppingConfig `mapstructure:"early_stopping"`
}

func (config *CollaborativeConfig) FullName() string {
	return "collaborative"
}

func (config *CollaborativeConfig) Hash(cfg *RecommendConfig) string {
	hash := md5.New()
	for _, expr := range cfg.DataSource.PositiveFeedbackTypes {
		hash.Write([]byte(expr.String()))
	}
	for _, expr := range cfg.DataSource.NegativeFeedbackTypes {
		hash.Write([]byte(expr.String()))
	}
	return hex.EncodeToString(hash.Sum(nil)[:])
}

type EarlyStoppingConfig struct {
	Patience int `mapstructure:"patience"`
}

type ExternalConfig struct {
	Name   string `mapstructure:"name" json:"name"`
	Script string `mapstructure:"script" json:"script"`
}

func (config *ExternalConfig) FullName() string {
	return "external/" + config.Name
}

func (config *ExternalConfig) Hash() string {
	hash := md5.New()
	hash.Write([]byte(config.Name))
	hash.Write([]byte(config.Script))
	return hex.EncodeToString(hash.Sum(nil)[:])
}

type ReplacementConfig struct {
	EnableReplacement        bool    `mapstructure:"enable_replacement"`
	PositiveReplacementDecay float64 `mapstructure:"positive_replacement_decay" validate:"gt=0"`
	ReadReplacementDecay     float64 `mapstructure:"read_replacement_decay" validate:"gt=0"`
}

type RankerConfig struct {
	Type             string              `mapstructure:"type" validate:"oneof=none fm llm"`
	Recommenders     []string            `mapstructure:"recommenders"`
	CacheExpire      time.Duration       `mapstructure:"cache_expire" validate:"gt=0"`
	FitPeriod        time.Duration       `mapstructure:"fit_period" validate:"gt=0"`
	FitEpoch         int                 `mapstructure:"fit_epoch" validate:"gt=0"`
	OptimizePeriod   time.Duration       `mapstructure:"optimize_period" validate:"gte=0"`
	OptimizeTrials   int                 `mapstructure:"optimize_trials" validate:"gt=0"`
	QueryTemplate    string              `mapstructure:"query_template"`
	DocumentTemplate string              `mapstructure:"document_template"`
	EarlyStopping    EarlyStoppingConfig `mapstructure:"early_stopping"`
	RerankerAPI      RerankerAPIConfig   `mapstructure:"reranker_api"`
}

type FallbackConfig struct {
	Recommenders []string `mapstructure:"recommenders"`
}

// LifecycleConfig is the top-level lifecycle-aware recall configuration.
type LifecycleConfig struct {
	Enabled bool          `mapstructure:"enabled"`
	CacheTTL time.Duration `mapstructure:"cache_ttl"` // TTL for lifecycle classification cache
}

// RecallPoolConfig describes a single recall pool for lifecycle-aware blending.
type RecallPoolConfig struct {
	Name           string   `mapstructure:"name" json:"name"`                             // pool name, e.g. "cold_start", "fatigue"
	LifecycleTypes []string `mapstructure:"lifecycle_types" json:"lifecycle_types"`       // which lifecycle types this pool serves
	BaseWeight    float64  `mapstructure:"base_weight" json:"base_weight"`             // base blend weight (0-1)
	Recommenders  []string `mapstructure:"recommenders" json:"recommenders"`             // recommender names in this pool
	ExploreRatio float64  `mapstructure:"explore_ratio" json:"explore_ratio"`         // exploration ratio (0-1)
}

// SupplyDemandConfig controls gender-based supply/demand balance.
type SupplyDemandConfig struct {
	Enabled            bool              `mapstructure:"enabled"`
	RedisAddr         string            `mapstructure:"redis_addr"`          // Redis address for exposure counting
	RedisPassword     string            `mapstructure:"redis_password"`
	GenderGroups      []string          `mapstructure:"gender_groups"`      // e.g. ["M","F","O"]
	ExposureLimits    map[string]int    `mapstructure:"exposure_limits"`    // per gender group, per day, e.g. {"F":100,"O":50}
	LowExposureThreshold int            `mapstructure:"low_exposure_threshold"` // trigger boost when exposure < this
	LowExposureBoost   float64         `mapstructure:"low_exposure_boost"`   // weight multiplier for low-exposure users
}

// FatigueConfig controls the fatigue-breaking mechanism.
type FatigueConfig struct {
	Enabled           bool    `mapstructure:"enabled"`
	TriggerSwipes    int     `mapstructure:"trigger_swipes"`    // consecutive swipes without match to trigger fatigue
	TriggerDays      int     `mapstructure:"trigger_days"`      // consecutive days without match to trigger fatigue
	RandomRatio     float64 `mapstructure:"random_ratio"`     // ratio of random perturbation
	DiversityCooldown int    `mapstructure:"diversity_cooldown"` // insert one diversity item every N recommendations
}

// QualityConfig controls quality filtering.
type QualityConfig struct {
	Enabled     bool    `mapstructure:"enabled"`
	RedisAddr  string  `mapstructure:"redis_addr"`
	RedisPassword string `mapstructure:"redis_password"`
	MinLikeRate float64 `mapstructure:"min_like_rate"` // minimum like rate for items (pre-computed in Item.Labels["like_rate"])
	MaxBlockRate float64 `mapstructure:"max_block_rate"` // maximum block rate (0-1)
}

// VIPConfig controls VIP user protection.
type VIPConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	VIPLabelKey   string `mapstructure:"vip_label_key"`    // User.Labels key for VIP tag, e.g. "tier"
	VIPLabelValue string `mapstructure:"vip_label_value"`  // value that indicates VIP, e.g. "vip"
	ManualOverride bool  `mapstructure:"manual_override"`   // allow admin to manually set VIP via label
}

// BehaviorConfig controls behavioral feature computation and explore/exploit injection.
type BehaviorConfig struct {
	Enabled           bool    `mapstructure:"enabled"`
	RedisAddr        string  `mapstructure:"redis_addr"`
	RedisPassword    string  `mapstructure:"redis_password"`
	MinSwipeCount    int     `mapstructure:"min_swipe_count"`     // minimum swipes before computing stats (default 10)
	StabilityWindow  int     `mapstructure:"stability_window"`    // window for stability calculation in swipes (default 20)
	StatsCacheTTL    int     `mapstructure:"stats_cache_ttl"`    // TTL in seconds for stats in Redis (default 300)
	DefaultExploreRatio float64 `mapstructure:"default_explore_ratio"` // fallback explore ratio for users without computed stats
}

// ItemStatsConfig controls item-level quality stats computation.
type ItemStatsConfig struct {
	Enabled          bool    `mapstructure:"enabled"`
	RedisAddr        string  `mapstructure:"redis_addr"`
	RedisPassword    string  `mapstructure:"redis_password"`
	MinExposureCount int     `mapstructure:"min_exposure_count"` // minimum exposures before computing stats
	StatsCacheTTL    int     `mapstructure:"stats_cache_ttl"`   // TTL in seconds
	MinLikeRate      float64 `mapstructure:"min_like_rate"`    // minimum like rate to not be filtered (0-1)
	MaxBlockRate     float64 `mapstructure:"max_block_rate"`   // maximum block rate to not be filtered (0-1)
}

type TracingConfig struct {
	EnableTracing     bool    `mapstructure:"enable_tracing"`
	Exporter          string  `mapstructure:"exporter" validate:"oneof=zipkin otlp otlphttp"`
	CollectorEndpoint string  `mapstructure:"collector_endpoint"`
	Sampler           string  `mapstructure:"sampler"`
	Ratio             float64 `mapstructure:"ratio"`
}

type OIDCConfig struct {
	Enable       bool   `mapstructure:"enable"`
	Issuer       string `mapstructure:"issuer"`
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	RedirectURL  string `mapstructure:"redirect_url" validate:"omitempty,endswith=/callback/oauth2"`
}

type RerankerAPIConfig struct {
	AuthToken string `mapstructure:"auth_token"`
	Model     string `mapstructure:"model"`
	URL       string `mapstructure:"url"`
}

// MMRConfig controls Maximum Marginal Relevance (MMR) diversity reranking.
type MMRConfig struct {
	Enabled      bool    `mapstructure:"enabled"`
	Lambda       float64 `mapstructure:"lambda"`        // diversity weight (0-1), higher = more diverse items
	WindowSize   int     `mapstructure:"window_size"`    // how many top items to check for similarity
	AgeDecay     float64 `mapstructure:"age_decay"`     // penalty for similar age (0-1)
	GenderDecay  float64 `mapstructure:"gender_decay"`  // penalty for same gender (0-1)
}

// SuccessRateConfig controls historical success-rate based reranking.
type SuccessRateConfig struct {
	Enabled       bool    `mapstructure:"enabled"`
	RedisAddr    string  `mapstructure:"redis_addr"`
	RedisPassword string `mapstructure:"redis_password"`
	MinExposures  int     `mapstructure:"min_exposures"`   // minimum exposures before boosting
	SuccessWeight float64 `mapstructure:"success_weight"`  // weight for success rate (0-1)
	MatchBoost   float64 `mapstructure:"match_boost"`    // boost multiplier for high match rate items
}

// SessionFatigueConfig controls real-time session-level fatigue tracking.
type SessionFatigueConfig struct {
	Enabled         bool    `mapstructure:"enabled"`
	RedisAddr       string  `mapstructure:"redis_addr"`
	RedisPassword   string  `mapstructure:"redis_password"`
	ResetOnMatch    bool    `mapstructure:"reset_on_match"`    // reset counter when match is recorded
	SwipeThreshold  int     `mapstructure:"swipe_threshold"`    // swipes before fatigue
	RecsThreshold   int     `mapstructure:"recs_threshold"`    // recs shown before fatigue
	DiversityBoost  float64 `mapstructure:"diversity_boost"`   // extra explore ratio when fatigued
}

// SocialGraphConfig controls the social-graph-based recommender.
type SocialGraphConfig struct {
	MatchThreshold       int      `mapstructure:"match_threshold"`        // switch to FoF when matches >= threshold (default 10)
	MaxFriends          int      `mapstructure:"max_friends"`             // max matched users to fetch (default 50)
	FoFMaxHops          int      `mapstructure:"fof_max_hops"`            // FoF traversal hops, 2 = FoF enabled (default 2)
	FoFMaxFriendsPerNode int     `mapstructure:"fof_max_friends_per_node"` // max 2-hop friends per 1-hop (default 30)
	MatchFeedbackType   string   `mapstructure:"match_feedback_type"`    // feedback type for matches (default "match")
	PositiveFeedbackTypes []string `mapstructure:"positive_feedback_types"` // positive interactions to propagate (default ["like"])
}

type OpenAIConfig struct {
	BaseURL             string `mapstructure:"base_url"`
	AuthToken           string `mapstructure:"auth_token"`
	ChatCompletionModel string `mapstructure:"chat_completion_model"`
	ChatCompletionRPM   int    `mapstructure:"chat_completion_rpm"`
	ChatCompletionTPM   int    `mapstructure:"chat_completion_tpm"`
	EmbeddingModel      string `mapstructure:"embedding_model"`
	EmbeddingDimensions int    `mapstructure:"embedding_dimensions"`
	EmbeddingRPM        int    `mapstructure:"embedding_rpm"`
	EmbeddingTPM        int    `mapstructure:"embedding_tpm"`
	LogFile             string `mapstructure:"log_file"`
}

type BlobConfig struct {
	URI   string          `mapstructure:"uri" validate:"required"`
	S3    S3Config        `mapstructure:"s3"`
	GCS   GCSConfig       `mapstructure:"gcs"`
	Azure AzureBlobConfig `mapstructure:"azure"`
}

type S3Config struct {
	Endpoint        string `mapstructure:"endpoint"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
}

type GCSConfig struct {
	CredentialsFile string `mapstructure:"credentials_file"`
}

type AzureBlobConfig struct {
	Endpoint         string `mapstructure:"endpoint"`
	AccountName      string `mapstructure:"account_name"`
	AccountKey       string `mapstructure:"account_key"`
	ConnectionString string `mapstructure:"connection_string"`
}

func GetDefaultConfig() *Config {
	return &Config{
		Database: DatabaseConfig{
			DataStore:  "sqlite://" + filepath.Join(MkDir(), "data.sqlite"),
			CacheStore: "sqlite://" + filepath.Join(MkDir(), "cache.sqlite"),
			MySQL: MySQLConfig{
				IsolationLevel:  "READ-UNCOMMITTED",
				MaxOpenConns:    0,
				MaxIdleConns:    0,
				ConnMaxLifetime: 0,
			},
			Postgres: SQLConfig{
				MaxOpenConns:    64,
				MaxIdleConns:    64,
				ConnMaxLifetime: time.Minute,
			},
			Redis: RedisConfig{
				MaxSearchResults: 10000,
			},
		},
		Master: MasterConfig{
			Port:            8086,
			Host:            "0.0.0.0",
			HttpPort:        8088,
			HttpHost:        "0.0.0.0",
			HttpCorsDomains: []string{".*"},
			HttpCorsMethods: []string{"GET", "POST", "PUT", "DELETE", "PATCH"},
			NumJobs:         1,
			MetaTimeout:     10 * time.Second,
		},
		Server: ServerConfig{
			DefaultN:       10,
			ClockError:     5 * time.Second,
			AutoInsertUser: true,
			AutoInsertItem: true,
			CacheExpire:    10 * time.Second,
		},
		Recommend: RecommendConfig{
			CacheSize:   100,
			CacheExpire: 72 * time.Hour,
			ContextSize: 100,
			Collaborative: CollaborativeConfig{
				Type:           "none",
				FitPeriod:      60 * time.Minute,
				FitEpoch:       100,
				OptimizePeriod: 0,
				OptimizeTrials: 10,
			},
			Replacement: ReplacementConfig{
				EnableReplacement:        false,
				PositiveReplacementDecay: 0.8,
				ReadReplacementDecay:     0.6,
			},
			Ranker: RankerConfig{
				Type:           "none",
				CacheExpire:    120 * time.Hour,
				FitPeriod:      60 * time.Minute,
				FitEpoch:       100,
				OptimizePeriod: 0,
				OptimizeTrials: 10,
				Recommenders:   []string{"latest"},
			},
			Lifecycle: LifecycleConfig{
				Enabled:  false,
				CacheTTL: 5 * time.Minute,
			},
			Fatigue: FatigueConfig{
				Enabled:           false,
				TriggerSwipes:     50,
				TriggerDays:       7,
				RandomRatio:      0.3,
				DiversityCooldown: 3,
			},
			Quality: QualityConfig{
				Enabled:      false,
				MinLikeRate:  0.05,
				MaxBlockRate: 0.1,
			},
			VIP: VIPConfig{
				Enabled:        false,
				VIPLabelKey:    "tier",
				VIPLabelValue:  "vip",
				ManualOverride: true,
			},
			Behavior: BehaviorConfig{
				Enabled:             false,
				MinSwipeCount:       10,
				StabilityWindow:     20,
				StatsCacheTTL:       300,
				DefaultExploreRatio: 0.2,
			},
			ItemStats: ItemStatsConfig{
				Enabled:          false,
				MinExposureCount: 5,
				StatsCacheTTL:    600,
				MinLikeRate:      0.05,
				MaxBlockRate:     0.1,
			},
			MMR: MMRConfig{
				Enabled:     false,
				Lambda:      0.3,
				WindowSize:  10,
				AgeDecay:    0.2,
				GenderDecay: 0.3,
			},
			SuccessRate: SuccessRateConfig{
				Enabled:      false,
				MinExposures: 10,
				SuccessWeight: 0.2,
				MatchBoost:   1.5,
			},
			SessionFatigue: SessionFatigueConfig{
				Enabled:        false,
				ResetOnMatch:   true,
				SwipeThreshold: 15,
				RecsThreshold:  20,
				DiversityBoost: 0.15,
			},
		},
		Tracing: TracingConfig{
			Exporter: "otlp",
			Sampler:  "always",
		},
		Blob: BlobConfig{
			URI: MkDir("blob"),
		},
	}
}

//go:embed config.toml
var ConfigTOML string

func (config *Config) Now() *time.Time {
	return new(time.Now().Add(config.Server.ClockError))
}

func (config *TracingConfig) NewTracerProvider() (trace.TracerProvider, error) {
	if !config.EnableTracing {
		return trace.NewNoopTracerProvider(), nil
	}

	var exporter tracesdk.SpanExporter
	var err error
	switch config.Exporter {
	case "zipkin":
		exporter, err = zipkin.New(config.CollectorEndpoint)
		if err != nil {
			return nil, errors.Trace(err)
		}
	case "otlp":
		client := otlptracegrpc.NewClient(otlptracegrpc.WithInsecure(), otlptracegrpc.WithEndpoint(config.CollectorEndpoint))
		exporter, err = otlptrace.New(context.TODO(), client)
		if err != nil {
			return nil, errors.Trace(err)
		}
	case "otlphttp":
		client := otlptracehttp.NewClient(otlptracehttp.WithInsecure(), otlptracehttp.WithEndpoint(config.CollectorEndpoint))
		exporter, err = otlptrace.New(context.TODO(), client)
		if err != nil {
			return nil, errors.Trace(err)
		}
	default:
		return nil, errors.NotSupportedf("exporter %s", config.Exporter)
	}

	var sampler tracesdk.Sampler
	switch config.Sampler {
	case "always":
		sampler = tracesdk.AlwaysSample()
	case "never":
		sampler = tracesdk.NeverSample()
	case "ratio":
		sampler = tracesdk.TraceIDRatioBased(config.Ratio)
	default:
		return nil, errors.NotSupportedf("sampler %s", config.Sampler)
	}

	return tracesdk.NewTracerProvider(
		tracesdk.WithSampler(sampler),
		tracesdk.WithBatcher(exporter),
		tracesdk.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("gorse"),
		)),
	), nil
}

func (config *TracingConfig) Equal(other TracingConfig) bool {
	if config == nil {
		return false
	}
	return config.EnableTracing == other.EnableTracing &&
		config.Exporter == other.Exporter &&
		config.CollectorEndpoint == other.CollectorEndpoint &&
		config.Sampler == other.Sampler &&
		config.Ratio == other.Ratio
}

func setDefault() {
	defaultConfig := GetDefaultConfig()
	// [database]
	viper.SetDefault("database.data_store", defaultConfig.Database.DataStore)
	viper.SetDefault("database.cache_store", defaultConfig.Database.CacheStore)
	// [database.mysql]
	viper.SetDefault("database.mysql.isolation_level", defaultConfig.Database.MySQL.IsolationLevel)
	viper.SetDefault("database.mysql.max_open_conns", defaultConfig.Database.MySQL.MaxOpenConns)
	viper.SetDefault("database.mysql.max_idle_conns", defaultConfig.Database.MySQL.MaxIdleConns)
	viper.SetDefault("database.mysql.conn_max_lifetime", defaultConfig.Database.MySQL.ConnMaxLifetime)
	// [database.postgres]
	viper.SetDefault("database.postgres.max_open_conns", defaultConfig.Database.Postgres.MaxOpenConns)
	viper.SetDefault("database.postgres.max_idle_conns", defaultConfig.Database.Postgres.MaxIdleConns)
	viper.SetDefault("database.postgres.conn_max_lifetime", defaultConfig.Database.Postgres.ConnMaxLifetime)
	// [database.redis]
	viper.SetDefault("database.redis.max_search_results", defaultConfig.Database.Redis.MaxSearchResults)
	// [master]
	viper.SetDefault("master.port", defaultConfig.Master.Port)
	viper.SetDefault("master.host", defaultConfig.Master.Host)
	viper.SetDefault("master.http_port", defaultConfig.Master.HttpPort)
	viper.SetDefault("master.http_host", defaultConfig.Master.HttpHost)
	viper.SetDefault("master.http_cors_domains", defaultConfig.Master.HttpCorsDomains)
	viper.SetDefault("master.http_cors_methods", defaultConfig.Master.HttpCorsMethods)
	viper.SetDefault("master.n_jobs", defaultConfig.Master.NumJobs)
	viper.SetDefault("master.meta_timeout", defaultConfig.Master.MetaTimeout)
	// [server]
	viper.SetDefault("server.api_key", defaultConfig.Server.APIKey)
	viper.SetDefault("server.default_n", defaultConfig.Server.DefaultN)
	viper.SetDefault("server.clock_error", defaultConfig.Server.ClockError)
	viper.SetDefault("server.auto_insert_user", defaultConfig.Server.AutoInsertUser)
	viper.SetDefault("server.auto_insert_item", defaultConfig.Server.AutoInsertItem)
	viper.SetDefault("server.cache_expire", defaultConfig.Server.CacheExpire)
	// [recommend]
	viper.SetDefault("recommend.cache_size", defaultConfig.Recommend.CacheSize)
	viper.SetDefault("recommend.cache_expire", defaultConfig.Recommend.CacheExpire)
	viper.SetDefault("recommend.context_size", defaultConfig.Recommend.ContextSize)
	// [recommend.collaborative]
	viper.SetDefault("recommend.collaborative.type", defaultConfig.Recommend.Collaborative.Type)
	viper.SetDefault("recommend.collaborative.fit_period", defaultConfig.Recommend.Collaborative.FitPeriod)
	viper.SetDefault("recommend.collaborative.fit_epoch", defaultConfig.Recommend.Collaborative.FitEpoch)
	viper.SetDefault("recommend.collaborative.optimize_period", defaultConfig.Recommend.Collaborative.OptimizePeriod)
	viper.SetDefault("recommend.collaborative.optimize_trials", defaultConfig.Recommend.Collaborative.OptimizeTrials)
	// [recommend.replacement]
	viper.SetDefault("recommend.replacement.enable_replacement", defaultConfig.Recommend.Replacement.EnableReplacement)
	viper.SetDefault("recommend.replacement.positive_replacement_decay", defaultConfig.Recommend.Replacement.PositiveReplacementDecay)
	viper.SetDefault("recommend.replacement.read_replacement_decay", defaultConfig.Recommend.Replacement.ReadReplacementDecay)
	// [recommend.ranker]
	viper.SetDefault("recommend.ranker.type", defaultConfig.Recommend.Ranker.Type)
	viper.SetDefault("recommend.ranker.cache_expire", defaultConfig.Recommend.Ranker.CacheExpire)
	viper.SetDefault("recommend.ranker.fit_period", defaultConfig.Recommend.Ranker.FitPeriod)
	viper.SetDefault("recommend.ranker.fit_epoch", defaultConfig.Recommend.Ranker.FitEpoch)
	viper.SetDefault("recommend.ranker.optimize_period", defaultConfig.Recommend.Ranker.OptimizePeriod)
	viper.SetDefault("recommend.ranker.optimize_trials", defaultConfig.Recommend.Ranker.OptimizeTrials)
	viper.SetDefault("recommend.ranker.recommenders", defaultConfig.Recommend.Ranker.Recommenders)
	// [recommend.fallback]
	viper.SetDefault("recommend.fallback", defaultConfig.Recommend.Fallback)
	// [recommend.lifecycle]
	viper.SetDefault("recommend.lifecycle.enabled", defaultConfig.Recommend.Lifecycle.Enabled)
	viper.SetDefault("recommend.lifecycle.cache_ttl", defaultConfig.Recommend.Lifecycle.CacheTTL)
	// [recommend.fatigue]
	viper.SetDefault("recommend.fatigue.enabled", defaultConfig.Recommend.Fatigue.Enabled)
	viper.SetDefault("recommend.fatigue.trigger_swipes", defaultConfig.Recommend.Fatigue.TriggerSwipes)
	viper.SetDefault("recommend.fatigue.trigger_days", defaultConfig.Recommend.Fatigue.TriggerDays)
	viper.SetDefault("recommend.fatigue.random_ratio", defaultConfig.Recommend.Fatigue.RandomRatio)
	viper.SetDefault("recommend.fatigue.diversity_cooldown", defaultConfig.Recommend.Fatigue.DiversityCooldown)
	// [recommend.quality]
	viper.SetDefault("recommend.quality.enabled", defaultConfig.Recommend.Quality.Enabled)
	viper.SetDefault("recommend.quality.min_like_rate", defaultConfig.Recommend.Quality.MinLikeRate)
	viper.SetDefault("recommend.quality.max_block_rate", defaultConfig.Recommend.Quality.MaxBlockRate)
	// [recommend.vip]
	viper.SetDefault("recommend.vip.enabled", defaultConfig.Recommend.VIP.Enabled)
	viper.SetDefault("recommend.vip.vip_label_key", defaultConfig.Recommend.VIP.VIPLabelKey)
	viper.SetDefault("recommend.vip.vip_label_value", defaultConfig.Recommend.VIP.VIPLabelValue)
	viper.SetDefault("recommend.vip.manual_override", defaultConfig.Recommend.VIP.ManualOverride)
	// [recommend.behavior]
	viper.SetDefault("recommend.behavior.enabled", defaultConfig.Recommend.Behavior.Enabled)
	viper.SetDefault("recommend.behavior.min_swipe_count", defaultConfig.Recommend.Behavior.MinSwipeCount)
	viper.SetDefault("recommend.behavior.stability_window", defaultConfig.Recommend.Behavior.StabilityWindow)
	viper.SetDefault("recommend.behavior.stats_cache_ttl", defaultConfig.Recommend.Behavior.StatsCacheTTL)
	viper.SetDefault("recommend.behavior.default_explore_ratio", defaultConfig.Recommend.Behavior.DefaultExploreRatio)
	// [recommend.item_stats]
	viper.SetDefault("recommend.item_stats.enabled", defaultConfig.Recommend.ItemStats.Enabled)
	viper.SetDefault("recommend.item_stats.min_exposure_count", defaultConfig.Recommend.ItemStats.MinExposureCount)
	viper.SetDefault("recommend.item_stats.stats_cache_ttl", defaultConfig.Recommend.ItemStats.StatsCacheTTL)
	viper.SetDefault("recommend.item_stats.min_like_rate", defaultConfig.Recommend.ItemStats.MinLikeRate)
	viper.SetDefault("recommend.item_stats.max_block_rate", defaultConfig.Recommend.ItemStats.MaxBlockRate)
	// [recommend.mmr]
	viper.SetDefault("recommend.mmr.enabled", defaultConfig.Recommend.MMR.Enabled)
	viper.SetDefault("recommend.mmr.lambda", defaultConfig.Recommend.MMR.Lambda)
	viper.SetDefault("recommend.mmr.window_size", defaultConfig.Recommend.MMR.WindowSize)
	viper.SetDefault("recommend.mmr.age_decay", defaultConfig.Recommend.MMR.AgeDecay)
	viper.SetDefault("recommend.mmr.gender_decay", defaultConfig.Recommend.MMR.GenderDecay)
	// [recommend.success_rate]
	viper.SetDefault("recommend.success_rate.enabled", defaultConfig.Recommend.SuccessRate.Enabled)
	viper.SetDefault("recommend.success_rate.min_exposures", defaultConfig.Recommend.SuccessRate.MinExposures)
	viper.SetDefault("recommend.success_rate.success_weight", defaultConfig.Recommend.SuccessRate.SuccessWeight)
	viper.SetDefault("recommend.success_rate.match_boost", defaultConfig.Recommend.SuccessRate.MatchBoost)
	// [recommend.session_fatigue]
	viper.SetDefault("recommend.session_fatigue.enabled", defaultConfig.Recommend.SessionFatigue.Enabled)
	viper.SetDefault("recommend.session_fatigue.reset_on_match", defaultConfig.Recommend.SessionFatigue.ResetOnMatch)
	viper.SetDefault("recommend.session_fatigue.swipe_threshold", defaultConfig.Recommend.SessionFatigue.SwipeThreshold)
	viper.SetDefault("recommend.session_fatigue.recs_threshold", defaultConfig.Recommend.SessionFatigue.RecsThreshold)
	viper.SetDefault("recommend.session_fatigue.diversity_boost", defaultConfig.Recommend.SessionFatigue.DiversityBoost)
	// [tracing]
	viper.SetDefault("tracing.exporter", defaultConfig.Tracing.Exporter)
	viper.SetDefault("tracing.sampler", defaultConfig.Tracing.Sampler)
	// [blob]
	viper.SetDefault("blob.uri", defaultConfig.Blob.URI)
}

type configBinding struct {
	key string
	env string
}

var bindings = []configBinding{
	{"database.cache_store", "GORSE_CACHE_STORE"},
	{"database.data_store", "GORSE_DATA_STORE"},
	{"database.table_prefix", "GORSE_TABLE_PREFIX"},
	{"database.cache_table_prefix", "GORSE_CACHE_TABLE_PREFIX"},
	{"database.data_table_prefix", "GORSE_DATA_TABLE_PREFIX"},
	{"master.port", "GORSE_MASTER_PORT"},
	{"master.host", "GORSE_MASTER_HOST"},
	{"master.ssl_mode", "GORSE_MASTER_SSL_MODE"},
	{"master.ssl_ca", "GORSE_MASTER_SSL_CA"},
	{"master.ssl_cert", "GORSE_MASTER_SSL_CERT"},
	{"master.ssl_key", "GORSE_MASTER_SSL_KEY"},
	{"master.http_port", "GORSE_MASTER_HTTP_PORT"},
	{"master.http_host", "GORSE_MASTER_HTTP_HOST"},
	{"master.n_jobs", "GORSE_MASTER_JOBS"},
	{"master.dashboard_user_name", "GORSE_DASHBOARD_USER_NAME"},
	{"master.dashboard_password", "GORSE_DASHBOARD_PASSWORD"},
	{"master.dashboard_auth_server", "GORSE_DASHBOARD_AUTH_SERVER"},
	{"master.dashboard_redacted", "GORSE_DASHBOARD_REDACTED"},
	{"master.admin_api_key", "GORSE_ADMIN_API_KEY"},
	{"server.api_key", "GORSE_SERVER_API_KEY"},
	{"oidc.enable", "GORSE_OIDC_ENABLE"},
	{"oidc.issuer", "GORSE_OIDC_ISSUER"},
	{"oidc.client_id", "GORSE_OIDC_CLIENT_ID"},
	{"oidc.client_secret", "GORSE_OIDC_CLIENT_SECRET"},
	{"oidc.redirect_url", "GORSE_OIDC_REDIRECT_URL"},
	{"blob.uri", "GORSE_BLOB_URI"},
	{"blob.s3.endpoint", "S3_ENDPOINT"},
	{"blob.s3.access_key_id", "S3_ACCESS_KEY_ID"},
	{"blob.s3.secret_access_key", "S3_SECRET_ACCESS_KEY"},
	{"blob.gcs.credentials_file", "GCS_CREDENTIALS_FILE"},
	{"blob.azure.endpoint", "AZURE_STORAGE_ENDPOINT"},
	{"blob.azure.account_name", "AZURE_STORAGE_ACCOUNT_NAME"},
	{"blob.azure.account_key", "AZURE_STORAGE_ACCOUNT_KEY"},
	{"blob.azure.connection_string", "AZURE_STORAGE_CONNECTION_STRING"},
	{"openai.base_url", "OPENAI_BASE_URL"},
	{"openai.auth_token", "OPENAI_AUTH_TOKEN"},
	{"openai.chat_completion_model", "OPENAI_CHAT_COMPLETION_MODEL"},
	{"recommend.ranker.reranker_api.url", "RERANKER_URL"},
	{"recommend.ranker.reranker_api.model", "RERANKER_MODEL"},
	{"recommend.ranker.reranker_api.auth_token", "RERANKER_AUTH_TOKEN"},
}

// LoadConfig loads configuration from toml file.
func LoadConfig(path string) (*Config, error) {
	// set default config
	setDefault()

	// bind environment bindings
	for _, binding := range bindings {
		err := viper.BindEnv(binding.key, binding.env)
		if err != nil {
			log.Logger().Fatal("failed to bind a Viper key to a ENV variable", zap.Error(err))
		}
	}

	// load config file if provided
	if path != "" {
		// check if file exist
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				log.Logger().Warn("config file not found, use default config", zap.String("path", path))
			} else {
				return nil, errors.Trace(err)
			}
		} else {
			// load config file
			viper.SetConfigFile(path)
			if err := viper.ReadInConfig(); err != nil {
				return nil, errors.Trace(err)
			}
		}
	} else {
		log.Logger().Info("no config file provided, use defaults and environment variables")
	}

	// unmarshal config file
	var conf Config
	if err := viper.Unmarshal(&conf); err != nil {
		return nil, errors.Trace(err)
	}

	// validate config file
	if err := conf.Validate(); err != nil {
		return nil, errors.Trace(err)
	}

	// apply table prefix
	if conf.Database.CacheTablePrefix == "" {
		conf.Database.CacheTablePrefix = conf.Database.TablePrefix
	}
	if conf.Database.DataTablePrefix == "" {
		conf.Database.DataTablePrefix = conf.Database.TablePrefix
	}
	return &conf, nil
}

func (config *Config) Validate() error {
	// Check non-personalized recommenders
	nonPersonalizedNames := mapset.NewSet[string]()
	for _, nonPersonalized := range config.Recommend.NonPersonalized {
		if nonPersonalizedNames.Contains(nonPersonalized.Name) {
			return errors.Errorf("non-personalized recommender %v is duplicated", nonPersonalized.Name)
		}
		nonPersonalizedNames.Add(nonPersonalized.Name)
	}

	// Check item-to-item recommenders
	itemToItemNames := mapset.NewSet[string]()
	for _, itemToItem := range config.Recommend.ItemToItem {
		if itemToItemNames.Contains(itemToItem.Name) {
			return errors.Errorf("item-to-item recommender %v is duplicated", itemToItem.Name)
		}
		itemToItemNames.Add(itemToItem.Name)
	}

	// Check recommender existence and collaborative enabled
	availableRecommenders := mapset.NewSet[string]()
	for _, rec := range config.Recommend.NonPersonalized {
		availableRecommenders.Add(rec.FullName())
	}
	for _, rec := range config.Recommend.ItemToItem {
		availableRecommenders.Add(rec.FullName())
	}
	for _, rec := range config.Recommend.UserToUser {
		availableRecommenders.Add(rec.FullName())
	}
	for _, rec := range config.Recommend.External {
		availableRecommenders.Add(rec.FullName())
	}
	availableRecommenders.Add("latest")
	if config.Recommend.Collaborative.Type != "none" {
		availableRecommenders.Add(config.Recommend.Collaborative.FullName())
	}
	checkRecommenders := func(recommenders []string) error {
		for _, recommender := range recommenders {
			if recommender == config.Recommend.Collaborative.FullName() && config.Recommend.Collaborative.Type == "none" {
				return errors.New("collaborative recommender is disabled")
			}
			if !availableRecommenders.Contains(recommender) {
				return errors.Errorf("recommender %v doesn't exist", recommender)
			}
		}
		return nil
	}

	validate := validator.New()
	if err := validate.RegisterValidation("data_store", func(fl validator.FieldLevel) bool {
		prefixes := []string{
			storage.MongoPrefix,
			storage.MongoSrvPrefix,
			storage.MySQLPrefix,
			storage.PostgresPrefix,
			storage.PostgreSQLPrefix,
			storage.ClickhousePrefix,
			storage.CHHTTPPrefix,
			storage.CHHTTPSPrefix,
			storage.SQLitePrefix,
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(fl.Field().String(), prefix) {
				return true
			}
		}
		return false
	}); err != nil {
		return errors.Trace(err)
	}
	if err := validate.RegisterValidation("cache_store", func(fl validator.FieldLevel) bool {
		prefixes := []string{
			storage.RedisPrefix,
			storage.RedissPrefix,
			storage.RedisClusterPrefix,
			storage.RedissClusterPrefix,
			storage.MongoPrefix,
			storage.MongoSrvPrefix,
			storage.MySQLPrefix,
			storage.PostgresPrefix,
			storage.PostgreSQLPrefix,
			storage.SQLitePrefix,
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(fl.Field().String(), prefix) {
				return true
			}
		}
		return false
	}); err != nil {
		return errors.Trace(err)
	}
	if err := validate.RegisterValidation("item_expr", func(fl validator.FieldLevel) bool {
		if fl.Field().String() == "" {
			// Empty expression is legal.
			return true
		}
		_, err := parser.Parse(fl.Field().String())
		return err == nil
	}); err != nil {
		return errors.Trace(err)
	}
	validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
		return strings.SplitN(fld.Tag.Get("mapstructure"), ",", 2)[0]
	})
	err := validate.Struct(config)
	if err != nil {
		// translate errors
		trans := ut.New(en.New()).GetFallback()
		if err := en_translations.RegisterDefaultTranslations(validate, trans); err != nil {
			return errors.Trace(err)
		}
		if err := validate.RegisterTranslation("data_store", trans, func(ut ut.Translator) error {
			return ut.Add("data_store", "unsupported data storage backend", true) // see universal-translator for details
		}, func(ut ut.Translator, fe validator.FieldError) string {
			t, _ := ut.T("data_store", fe.Field())
			return t
		}); err != nil {
			return errors.Trace(err)
		}
		if err := validate.RegisterTranslation("cache_store", trans, func(ut ut.Translator) error {
			return ut.Add("cache_store", "unsupported cache storage backend", true) // see universal-translator for details
		}, func(ut ut.Translator, fe validator.FieldError) string {
			t, _ := ut.T("cache_store", fe.Field())
			return t
		}); err != nil {
			return errors.Trace(err)
		}
		if err := validate.RegisterTranslation("item_expr", trans, func(ut ut.Translator) error {
			return ut.Add("item_expr", "invalid item expression", true)
		}, func(ut ut.Translator, fe validator.FieldError) string {
			t, _ := ut.T("item_expr", fe.Field())
			return t
		}); err != nil {
			return errors.Trace(err)
		}
		errs := err.(validator.ValidationErrors)
		for _, e := range errs {
			return errors.New(e.Translate(trans))
		}
	}

	if len(config.Recommend.Ranker.Recommenders) == 0 {
		return errors.New("ranker.recommenders must not be empty")
	}
	if config.Recommend.Ranker.Type == "none" && len(config.Recommend.Ranker.Recommenders) > 1 {
		return errors.New("ranker.recommenders must contain at most one recommender when ranker.type is none")
	}
	if err := checkRecommenders(config.Recommend.Ranker.Recommenders); err != nil {
		return err
	}
	if err := checkRecommenders(config.Recommend.Fallback.Recommenders); err != nil {
		return err
	}
	return nil
}

var RootDir string

// MkDir creates a directory under Gorse home directory.
func MkDir(elem ...string) string {
	if RootDir == "" {
		RootDir = filepath.Join(lo.Must(os.UserHomeDir()), ".gorse", "var", "lib")
	}
	path := filepath.Join(RootDir, filepath.Join(elem...))
	lo.Must0(os.MkdirAll(path, 0755))
	return path
}

module github.com/rakunlabs/pika

go 1.26

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/bmatcuk/doublestar/v4 v4.10.0
	github.com/dgraph-io/badger/v4 v4.9.1
	github.com/goccy/go-yaml v1.19.2
	github.com/modelcontextprotocol/go-sdk v1.7.0
	github.com/nats-io/nats.go v1.50.0
	github.com/rakunlabs/ada v0.4.7
	github.com/rakunlabs/ada/handler/folder v0.4.7
	github.com/rakunlabs/ada/middleware/auth v0.4.9
	github.com/rakunlabs/ada/middleware/cors v0.4.7
	github.com/rakunlabs/ada/middleware/log v0.4.7
	github.com/rakunlabs/ada/middleware/ratelimit v0.4.7
	github.com/rakunlabs/ada/middleware/recover v0.4.7
	github.com/rakunlabs/ada/middleware/requestid v0.4.7
	github.com/rakunlabs/ada/middleware/server v0.4.7
	github.com/rakunlabs/ada/middleware/telemetry v0.4.7
	github.com/rakunlabs/alan v0.5.0
	github.com/rakunlabs/bw v0.3.14
	github.com/rakunlabs/chu v0.4.7
	github.com/rakunlabs/chu/loader/external/loaderconsul v0.0.0-20260528194425-7d41650a9186
	github.com/rakunlabs/chu/loader/external/loadergcpparameter v0.0.0-20260528194425-7d41650a9186
	github.com/rakunlabs/chu/loader/external/loadergcpsecret v0.0.0-20260528194425-7d41650a9186
	github.com/rakunlabs/chu/loader/external/loadervault v0.0.0-20260528194425-7d41650a9186
	github.com/rakunlabs/into v0.5.3
	github.com/rakunlabs/logi v0.4.5
	github.com/rakunlabs/ok v0.1.0
	github.com/rakunlabs/query v0.4.10
	github.com/rakunlabs/tell v0.1.5
	github.com/rakunlabs/tummy v0.1.2
	github.com/redis/go-redis/v9 v9.18.0
	github.com/rytsh/mugo v0.9.2
	github.com/twmb/franz-go v1.20.7
	golang.org/x/crypto v0.51.0
)

require (
	cloud.google.com/go/auth v0.18.2 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	cloud.google.com/go/iam v1.6.0 // indirect
	cloud.google.com/go/parametermanager v0.4.0 // indirect
	cloud.google.com/go/secretmanager v1.16.0 // indirect
	dario.cat/mergo v1.0.2 // indirect
	github.com/Masterminds/goutils v1.1.1 // indirect
	github.com/Masterminds/semver/v3 v3.4.0 // indirect
	github.com/Masterminds/sprig/v3 v3.3.0 // indirect
	github.com/armon/go-metrics v0.4.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cli/safeexec v1.0.1 // indirect
	github.com/dgraph-io/ristretto/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/gomarkdown/markdown v0.0.0-20250810172220-2e2c11897d1a // indirect
	github.com/google/flatbuffers v25.2.10+incompatible // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.14 // indirect
	github.com/googleapis/gax-go/v2 v2.20.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/hashicorp/consul/api v1.33.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-envparse v0.1.0 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/go-immutable-radix v1.3.1 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.8 // indirect
	github.com/hashicorp/go-rootcerts v1.0.2 // indirect
	github.com/hashicorp/go-secure-stdlib/parseutil v0.2.0 // indirect
	github.com/hashicorp/go-secure-stdlib/strutil v0.1.2 // indirect
	github.com/hashicorp/go-sockaddr v1.0.7 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/hashicorp/hcl v1.0.1-vault-7 // indirect
	github.com/hashicorp/serf v0.10.1 // indirect
	github.com/hashicorp/vault/api v1.22.0 // indirect
	github.com/huandu/xstrings v1.5.0 // indirect
	github.com/jaswdr/faker v1.19.1 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/klauspost/cpuid/v2 v2.2.11 // indirect
	github.com/lmittmann/tint v1.1.3 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/nats-io/nkeys v0.4.15 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/oklog/ulid/v2 v2.1.1 // indirect
	github.com/pierrec/lz4/v4 v4.1.25 // indirect
	github.com/quic-go/quic-go v0.59.0 // indirect
	github.com/rakunlabs/cache v0.3.3 // indirect
	github.com/rs/zerolog v1.34.0 // indirect
	github.com/ryanuber/go-glob v1.0.0 // indirect
	github.com/rytsh/liz/file v0.1.4 // indirect
	github.com/rytsh/liz/shutdown v0.1.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/tdewolff/minify/v2 v2.24.3 // indirect
	github.com/tdewolff/parse/v2 v2.8.3 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.12.0 // indirect
	github.com/twmb/tlscfg v1.3.0 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/worldline-go/struct2 v1.4.0 // indirect
	github.com/xhit/go-str2duration/v2 v2.1.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.61.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.68.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/runtime v0.69.0 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.opentelemetry.io/proto/otlp v1.10.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/exp v0.0.0-20250808145144-a408d31f581a // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/api v0.273.1 // indirect
	google.golang.org/genproto v0.0.0-20260319201613-d00831a3d3e7 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.81.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

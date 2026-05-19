// Single AWS S3 module covering both load (S3 GetObject) and
// change-driven reloads (S3 → EventBridge → SQS). The former
// providers/s3events sub-module is merged here to reduce module
// maintenance overhead; both providers are still separately importable
// as github.com/fastabc/fastconf/providers/s3 and
// github.com/fastabc/fastconf/providers/s3/s3events.
module github.com/fastabc/fastconf/providers/s3

go 1.22

require (
	github.com/aws/aws-sdk-go-v2 v1.32.6
	github.com/aws/aws-sdk-go-v2/credentials v1.17.47
	github.com/aws/aws-sdk-go-v2/service/s3 v1.71.0
	github.com/aws/aws-sdk-go-v2/service/sqs v1.37.2
	github.com/aws/smithy-go v1.22.1
	github.com/fastabc/fastconf v0.0.0
)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.6.7 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.25 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.25 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.3.25 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.12.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.4.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.12.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.18.6 // indirect
	github.com/kr/text v0.2.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/fastabc/fastconf => ../..

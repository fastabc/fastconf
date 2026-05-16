// Separate Go module so users that don't need S3-change-driven reloads
// can avoid pulling the AWS SQS SDK into their build. This sub-module
// is the "watch" half of the S3 story; pair it with
// providers/s3 (load-only) to get full load + change-driven reloads.
module github.com/fastabc/fastconf/providers/s3events

go 1.26.2

require (
	github.com/aws/aws-sdk-go-v2 v1.32.6
	github.com/aws/aws-sdk-go-v2/credentials v1.17.47
	github.com/aws/aws-sdk-go-v2/service/sqs v1.37.2
	github.com/fastabc/fastconf v0.0.0
)

require (
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.25 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.25 // indirect
	github.com/aws/smithy-go v1.22.1 // indirect
)

replace github.com/fastabc/fastconf => ../..

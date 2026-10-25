package sns

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

func validateSource(cfg Config) error {
	switch cfg.Source {
	case S3:
		if cfg.Bucket != "" && cfg.Key != "" {
			return nil
		}
	case AppConfig:
		if cfg.Application != "" && cfg.Environment != "" && cfg.Profile != "" {
			return nil
		}
	case ParameterStore:
		if strings.HasPrefix(cfg.Path, "/") && strings.Trim(cfg.Path, "/") != "" {
			return nil
		}
	}
	return errors.New("fastconf/sns: source and its resource identifiers are required")
}

// SNS envelopes and raw delivery are documented at:
// https://docs.aws.amazon.com/sns/latest/dg/sns-large-payload-raw-message-delivery.html
func match(cfg Config, body string) (bool, error) {
	if len(body) > 1<<20 {
		return false, errors.New("notification exceeds 1 MiB")
	}
	var outer struct {
		Type, TopicArn string
		Message        json.RawMessage
	}
	if err := json.Unmarshal([]byte(body), &outer); err != nil {
		return false, err
	}
	if outer.Type == "Notification" {
		if cfg.TopicARN != "" && outer.TopicArn != cfg.TopicARN {
			return false, nil
		}
		payload := outer.Message
		if len(payload) > 0 && payload[0] == '"' {
			if err := json.Unmarshal(payload, &body); err != nil {
				return false, err
			}
		} else {
			body = string(payload)
		}
	}
	if !json.Valid([]byte(body)) || strings.TrimSpace(body) == "null" {
		return false, errors.New("invalid notification object")
	}
	switch cfg.Source {
	case S3:
		return matchS3(cfg, []byte(body))
	case ParameterStore:
		var event struct {
			Source     string `json:"source"`
			DetailType string `json:"detail-type"`
			Detail     struct{ Name string }
		}
		if err := json.Unmarshal([]byte(body), &event); err != nil {
			return false, err
		}
		if event.Source == "" || event.DetailType == "" {
			return false, errors.New("missing EventBridge source or detail-type")
		}
		if event.Source == "aws.ssm" && event.DetailType == "Parameter Store Change" && event.Detail.Name == "" {
			return false, errors.New("missing parameter identity")
		}
		root := strings.TrimRight(cfg.Path, "/")
		return event.Source == "aws.ssm" && event.DetailType == "Parameter Store Change" &&
			(event.Detail.Name == root || strings.HasPrefix(event.Detail.Name, root+"/")), nil
	case AppConfig:
		var event struct {
			Type                                           string
			Application, Environment, ConfigurationProfile struct{ Id string }
		}
		if err := json.Unmarshal([]byte(body), &event); err != nil {
			return false, err
		}
		if event.Type == "" {
			return false, errors.New("missing deployment event type")
		}
		mutation := event.Type == "OnDeploymentStart" || event.Type == "OnDeploymentComplete" || event.Type == "OnDeploymentRolledBack"
		if mutation && (event.Application.Id == "" || event.Environment.Id == "" || event.ConfigurationProfile.Id == "") {
			return false, errors.New("missing deployment identity")
		}
		return mutation && event.Application.Id == cfg.Application && event.Environment.Id == cfg.Environment && event.ConfigurationProfile.Id == cfg.Profile, nil
	}
	return false, errors.New("unsupported source")
}

// Native S3 keys are form-URL-encoded, unlike EventBridge keys:
// https://docs.aws.amazon.com/AmazonS3/latest/userguide/notification-content-structure.html
func matchS3(cfg Config, body []byte) (bool, error) {
	type object struct {
		Bucket struct{ Name string }
		Object struct{ Key string }
	}
	var event struct {
		Service, Event string
		Source         string `json:"source"`
		DetailType     string `json:"detail-type"`
		Detail         object
		Records        []struct {
			EventSource, EventName string
			S3                     object
		}
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return false, err
	}
	if event.Service == "Amazon S3" && event.Event == "s3:TestEvent" {
		return false, nil
	}
	if event.Source == "" && event.Records == nil {
		return false, errors.New("missing S3 notification schema")
	}
	matched := false
	for _, record := range event.Records {
		if record.EventSource != "aws:s3" {
			continue
		}
		if record.EventName == "" || record.S3.Bucket.Name == "" || record.S3.Object.Key == "" {
			return false, errors.New("missing S3 record identity or event name")
		}
		key, err := url.QueryUnescape(record.S3.Object.Key)
		if err != nil {
			return false, err
		}
		mutation := strings.HasPrefix(record.EventName, "ObjectCreated:") || strings.HasPrefix(record.EventName, "ObjectRemoved:") || record.EventName == "ObjectRestore:Completed"
		matched = matched || mutation && record.S3.Bucket.Name == cfg.Bucket && key == cfg.Key
	}
	if event.Source == "aws.s3" {
		if event.DetailType == "" || event.Detail.Bucket.Name == "" || event.Detail.Object.Key == "" {
			return false, errors.New("missing S3 EventBridge identity or detail-type")
		}
		switch event.DetailType {
		case "Object Created", "Object Deleted", "Object Restore Completed", "Object Tags Added", "Object Tags Deleted", "Object ACL Updated":
			matched = matched || event.Detail.Bucket.Name == cfg.Bucket && event.Detail.Object.Key == cfg.Key
		}
	}
	return matched, nil
}

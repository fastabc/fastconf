package azservicebus

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// Only EventGridSchema is accepted. CloudEvents uses different source/type
// fields and must not silently appear as an unrelated event.
// https://learn.microsoft.com/azure/event-grid/event-schema-app-configuration
// https://learn.microsoft.com/azure/event-grid/event-schema-blob-storage
func match(cfg Config, body []byte) (bool, error) {
	if len(body) > 1<<20 {
		return false, errors.New("notification exceeds 1 MiB")
	}
	type event struct {
		Topic, EventType string
		Data             struct{ Key, Label, API, URL string }
	}
	var events []event
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false, errors.New("empty event")
	}
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &events); err != nil {
			return false, err
		}
	} else {
		var one event
		if err := json.Unmarshal(trimmed, &one); err != nil {
			return false, err
		}
		events = []event{one}
	}
	matched := false
	for _, ev := range events {
		if ev.EventType == "" || ev.Topic == "" {
			return false, errors.New("EventGridSchema eventType and topic are required")
		}
		if !strings.EqualFold(ev.Topic, cfg.ResourceID) {
			continue
		}
		switch cfg.Source {
		case AppConfiguration:
			if ev.EventType == "Microsoft.AppConfiguration.KeyValueModified" || ev.EventType == "Microsoft.AppConfiguration.KeyValueDeleted" {
				if ev.Data.Key == "" {
					return false, errors.New("missing setting identity")
				}
				matched = matched || strings.HasPrefix(ev.Data.Key, cfg.Prefix) && ev.Data.Label == cfg.Label
			}
		case Blob:
			if ev.EventType != "Microsoft.Storage.BlobCreated" && ev.EventType != "Microsoft.Storage.BlobDeleted" {
				continue
			}
			// HNS/SFTP creation can precede a committed document.
			if ev.Data.API == "CreateFile" || ev.Data.API == "SftpCreate" || ev.Data.API == "SftpWrite" {
				continue
			}
			// The blob URL has URL escaping semantics; subject is publisher-defined.
			object, err := url.Parse(ev.Data.URL)
			if err != nil || object.Host == "" || (object.Scheme != "https" && object.Scheme != "http") {
				return false, errors.New("invalid blob URL")
			}
			matched = matched || object.Path == "/"+cfg.Container+"/"+cfg.Key
		}
	}
	return matched, nil
}

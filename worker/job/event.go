package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// source is one uploaded video named by a queue message.
type source struct {
	Bucket string
	Key    string
}

// s3Event is the part of an S3 event notification the worker reads.
// https://docs.aws.amazon.com/AmazonS3/latest/userguide/notification-content-structure.html
type s3Event struct {
	Records []struct {
		EventName string `json:"eventName"`
		S3        struct {
			Bucket struct {
				Name string `json:"name"`
			} `json:"bucket"`
			Object struct {
				Key string `json:"key"`
			} `json:"object"`
		} `json:"s3"`
	} `json:"Records"`
}

// parseEvent returns the objects created by an S3 event notification. It
// returns none, without error, for a valid notification that creates nothing,
// such as the s3:TestEvent S3 sends when notifications are first configured.
func parseEvent(body string) ([]source, error) {
	var event s3Event
	if err := json.Unmarshal([]byte(body), &event); err != nil {
		return nil, fmt.Errorf("decode s3 event: %w", err)
	}

	var sources []source
	for _, record := range event.Records {
		if !strings.HasPrefix(record.EventName, "ObjectCreated:") {
			continue
		}
		// S3 URL-encodes the key in notifications, with "+" for a space.
		key, err := url.QueryUnescape(record.S3.Object.Key)
		if err != nil {
			return nil, fmt.Errorf("decode object key %q: %w", record.S3.Object.Key, err)
		}
		bucket := record.S3.Bucket.Name
		if bucket == "" || key == "" {
			return nil, errors.New("s3 event record has no bucket or key")
		}
		sources = append(sources, source{Bucket: bucket, Key: key})
	}
	return sources, nil
}

//go:build !js

package app

import (
	"os"
	"strconv"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
)

func configureStreaming(config *nextgo.Config) {
	for _, field := range []struct {
		name  string
		value *int
	}{
		{"NEXTGO_DEMO_STREAM_MAX_STREAMS", &config.Streaming.MaxStreams},
		{"NEXTGO_DEMO_STREAM_MAX_LOADERS", &config.Streaming.MaxLoaders},
	} {
		if text, present := os.LookupEnv(field.name); present {
			value, err := strconv.Atoi(text)
			if err != nil || value < 1 {
				panic(field.name + " must be a positive integer")
			}
			*field.value = value
		}
	}
	if text, present := os.LookupEnv("NEXTGO_DEMO_STREAM_TIMEOUT"); present {
		value, err := time.ParseDuration(text)
		if err != nil || value <= 0 {
			panic("NEXTGO_DEMO_STREAM_TIMEOUT must be a positive duration")
		}
		config.Streaming.Timeout = value
	}
}

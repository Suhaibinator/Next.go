package cache

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBypassNeverReadsPublishesOrJoinsPublicWork(t *testing.T) {
	s := New(4)
	normal := context.Background()
	preview := WithBypass(normal)
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		value, err := GetOrLoad(normal, s, "same", Options{TTL: time.Hour}, func(context.Context) (string, error) { close(started); <-release; return "published", nil })
		if err == nil && value != "published" {
			err = context.Canceled
		}
		done <- err
	}()
	<-started
	for i := 0; i < 2; i++ {
		value, info, err := Lookup(preview, s, "same", Options{TTL: time.Hour}, func(ctx context.Context) (string, error) {
			if !Bypassed(ctx) {
				t.Error("lost bypass")
			}
			return "draft", nil
		})
		if err != nil || value != "draft" || info.State != Bypass || info.FreshFor != 0 {
			t.Fatal(value, info, err)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	value, err := GetOrLoad(normal, s, "same", Options{TTL: time.Hour}, func(context.Context) (string, error) { t.Error("public cache overwritten"); return "wrong", nil })
	if err != nil || value != "published" {
		t.Fatal(value, err)
	}
	_, err = GetOrLoad(preview, s, "same", Options{}, func(context.Context) (string, error) { panic("failure") })
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatal(err)
	}
}
func TestBypassHonorsStoreAdmissionAndRequestCancellation(t *testing.T) {
	s := New(4, WithMaxConcurrentLoads(1))
	ctx, cancel := context.WithCancel(WithBypass(context.Background()))
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := GetOrLoad(ctx, s, "first", Options{}, func(ctx context.Context) (int, error) { close(started); <-release; return 1, nil })
		done <- err
	}()
	<-started
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := GetOrLoad(canceled, s, "second", Options{}, func(context.Context) (int, error) { t.Error("canceled loader admitted"); return 2, nil }); err != context.Canceled {
		t.Fatal(err)
	}
	cancel()
	close(release)
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := GetOrLoad(WithBypass(context.Background()), s, "third", Options{}, func(context.Context) (int, error) { return 3, nil }); err != nil {
		t.Fatal(err)
	}
}

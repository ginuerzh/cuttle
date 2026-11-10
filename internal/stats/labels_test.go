package stats

import (
	"context"
	"testing"
)

func TestSyncLabelsUpsertsAndKeepsDeleted(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.SyncLabels(ctx, map[string]map[string]string{
		"provider": {"p_0000000001": "alpha", "p_0000000002": "beta"},
		"node":     {"n_0000000001": "n1"},
	}); err != nil {
		t.Fatal(err)
	}

	// A rename updates the label; an entity that disappears from the config
	// keeps its last known name instead of losing its history.
	if err := s.SyncLabels(ctx, map[string]map[string]string{
		"provider": {"p_0000000001": "alpha-renamed"},
	}); err != nil {
		t.Fatal(err)
	}

	labels, err := s.Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := labels["provider"]["p_0000000001"]; got != "alpha-renamed" {
		t.Fatalf("renamed provider label = %q, want alpha-renamed", got)
	}
	if got := labels["provider"]["p_0000000002"]; got != "beta" {
		t.Fatalf("deleted provider label = %q, want the last known beta", got)
	}
	if got := labels["node"]["n_0000000001"]; got != "n1" {
		t.Fatalf("node label = %q, want n1", got)
	}
	if n := len(labels["provider"]); n != 2 {
		t.Fatalf("provider labels = %d, want 2", n)
	}
}

func TestSyncLabelsIgnoresProviderKeysAndEmptyValues(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// Provider keys are identified only inside their provider: they never
	// appear in stats rows and are not labelled.
	if err := s.SyncLabels(ctx, map[string]map[string]string{
		"provider_key": {"pk_0000000001": "primary"},
		"provider":     {"": "nameless", "p_0000000001": ""},
	}); err != nil {
		t.Fatal(err)
	}

	labels, err := s.Labels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := labels["provider_key"]; ok {
		t.Fatalf("provider_key labels = %+v, want none", labels["provider_key"])
	}
	if _, ok := labels["provider"]; ok {
		t.Fatalf("provider labels = %+v, want none", labels["provider"])
	}
}

func TestLabelsEmptyStore(t *testing.T) {
	s := newStore(t)

	labels, err := s.Labels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if labels == nil || len(labels) != 0 {
		t.Fatalf("labels = %+v, want an empty map", labels)
	}
}

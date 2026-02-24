package main

import (
	"testing"
)

func TestMessageGenerators(t *testing.T) {
	poison := poisonMessages()
	if len(poison) == 0 {
		t.Errorf("expected poison messages")
	}

	downstream := downstreamMessages()
	if len(downstream) == 0 {
		t.Errorf("expected downstream messages")
	}

	auth := authMessages()
	if len(auth) == 0 {
		t.Errorf("expected auth messages")
	}

	cfg := configMessages()
	if len(cfg) == 0 {
		t.Errorf("expected config messages")
	}

	network := networkMessages()
	if len(network) == 0 {
		t.Errorf("expected network messages")
	}

	resource := resourceMessages()
	if len(resource) == 0 {
		t.Errorf("expected resource messages")
	}

	transient := transientMessages()
	if len(transient) == 0 {
		t.Errorf("expected transient messages")
	}

	all := generateMessages()
	if len(all) == 0 {
		t.Errorf("expected to generate messages")
	}
}

package tests

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/GillMacedo/straykeys/examples/billing"
	"gopkg.in/yaml.v3"
)

// TestBillingWithRealDecoder backs the README: the example service loads
// config.prod.yaml without an error and silently drops the four keys
// straykeys reports, and yaml.v3's own strict mode names the same lines.
func TestBillingWithRealDecoder(t *testing.T) {
	c, err := billing.Load("../examples/billing/config.prod.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Database.MaxOpenConns != 0 || c.Retry.Backoff != 0 || c.Invoices.Notify[0].OnFailed {
		t.Errorf("expected the misspelled settings to be dropped: %+v", c)
	}
	if c.Retry.Attempts != 5 || c.Invoices.DueDays != 14 {
		t.Errorf("expected the correct settings to load: %+v", c)
	}
	data, _ := os.ReadFile("../examples/billing/config.prod.yaml")
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var strict billing.Config
	err = dec.Decode(&strict)
	if err == nil {
		t.Fatal("strict decode succeeded")
	}
	for _, line := range []string{"line 4:", "line 8:", "line 14:", "line 18:"} {
		if !strings.Contains(err.Error(), line) {
			t.Errorf("strict error doesn't mention %s: %v", line, err)
		}
	}
	if n := strings.Count(err.Error(), "not found in type"); n != 4 {
		t.Errorf("strict decode found %d unknown fields, want 4: %v", n, err)
	}
	if _, err := billing.Load("../examples/billing/config.dev.yaml"); err != nil {
		t.Errorf("dev config: %v", err)
	}
}

// TestBillingConfigMapWithRealDecoder: the ConfigMap's conn_timeout: 5 makes
// yaml.v3 refuse the whole config, as straykeys says.
func TestBillingConfigMapWithRealDecoder(t *testing.T) {
	data, _ := os.ReadFile("../examples/billing/deploy/configmap.yaml")
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var cm struct {
		Data map[string]string `yaml:"data"`
	}
	if err := dec.Decode(&cm); err != nil {
		t.Fatal(err)
	}
	var c billing.Config
	err := yaml.Unmarshal([]byte(cm.Data["config.yaml"]), &c)
	if err == nil || !strings.Contains(err.Error(), "time.Duration") {
		t.Errorf("want a time.Duration error, got %v", err)
	}
}

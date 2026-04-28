package e2e_test

import (
	"io"
	"testing"
)

// createDefaultProfile spins up a dummy "active" profile so the auto-update
// endpoints have somewhere to write. Returns the resulting profile id.
func createDefaultProfile(t *testing.T, base string) string {
	t.Helper()
	body := `{"action":"create","profile":{"id":"","nickname":"AU","config":{"domain":"au.example","key":"k","resolvers":["127.0.0.1:9999"],"queryMode":"single","rateLimit":0}}}`
	resp := postJSON(t, base+"/api/profiles", body)
	resp.Body.Close()
	m := decodeJSON(t, getJSON(t, base+"/api/profiles"))
	profs, ok := m["profiles"].([]any)
	if !ok || len(profs) == 0 {
		t.Fatalf("profile not created, got %v", m["profiles"])
	}
	return profs[0].(map[string]any)["id"].(string)
}

func TestE2E_AutoUpdate_GetEmpty(t *testing.T) {
	base, _ := startWebServer(t)
	createDefaultProfile(t, base)

	resp := getJSON(t, base+"/api/auto-update")
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, body)
	}
	m := decodeJSON(t, resp)
	chans, _ := m["channels"].([]any)
	if len(chans) != 0 {
		t.Errorf("expected empty channels, got %v", chans)
	}
	if d, _ := m["defaultIntervalSeconds"].(float64); int(d) != 60 {
		t.Errorf("defaultIntervalSeconds = %v, want 60", m["defaultIntervalSeconds"])
	}
}

func TestE2E_AutoUpdate_ToggleAddsAndRemoves(t *testing.T) {
	base, _ := startWebServer(t)
	createDefaultProfile(t, base)

	// First toggle: add.
	resp := postJSON(t, base+"/api/auto-update/toggle", `{"channel":"thefeed1"}`)
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("toggle add: %d body=%s", resp.StatusCode, body)
	}
	m := decodeJSON(t, resp)
	if m["enabled"] != true {
		t.Errorf("first toggle should set enabled=true, got %v", m["enabled"])
	}
	chans := m["channels"].([]any)
	if len(chans) != 1 || chans[0] != "thefeed1" {
		t.Errorf("channels after add = %v, want [thefeed1]", chans)
	}

	// Second toggle: remove.
	resp2 := postJSON(t, base+"/api/auto-update/toggle", `{"channel":"thefeed1"}`)
	m2 := decodeJSON(t, resp2)
	if m2["enabled"] != false {
		t.Errorf("second toggle should set enabled=false, got %v", m2["enabled"])
	}
	chans2 := m2["channels"]
	if list, ok := chans2.([]any); !ok || len(list) != 0 {
		t.Errorf("channels after remove = %v, want []", chans2)
	}
}

func TestE2E_AutoUpdate_TogglesAtSign(t *testing.T) {
	base, _ := startWebServer(t)
	createDefaultProfile(t, base)

	// Add with "@chan" — server must store stripped form.
	postJSON(t, base+"/api/auto-update/toggle", `{"channel":"@chan"}`).Body.Close()

	m := decodeJSON(t, getJSON(t, base+"/api/auto-update"))
	chans := m["channels"].([]any)
	if len(chans) != 1 || chans[0] != "chan" {
		t.Errorf("channels = %v, want [chan]", chans)
	}

	// Toggle with bare form should remove the same entry.
	resp := postJSON(t, base+"/api/auto-update/toggle", `{"channel":"chan"}`)
	m2 := decodeJSON(t, resp)
	if m2["enabled"] != false {
		t.Errorf("expected enabled=false, got %v", m2["enabled"])
	}
}

func TestE2E_AutoUpdate_PostReplacesList(t *testing.T) {
	base, _ := startWebServer(t)
	createDefaultProfile(t, base)

	// POST with normalisation cases: leading @, dupes, whitespace.
	body := `{"channels":["@a","b","@a","  c  ",""],"intervalSeconds":120}`
	resp := postJSON(t, base+"/api/auto-update", body)
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST: %d body=%s", resp.StatusCode, body)
	}
	m := decodeJSON(t, resp)
	chans := m["channels"].([]any)
	want := []string{"a", "b", "c"}
	if len(chans) != len(want) {
		t.Fatalf("channels len = %d, want %d (%v)", len(chans), len(want), chans)
	}
	for i, w := range want {
		if chans[i] != w {
			t.Errorf("channels[%d] = %v, want %q", i, chans[i], w)
		}
	}
	if iv, _ := m["intervalSeconds"].(float64); int(iv) != 120 {
		t.Errorf("intervalSeconds = %v, want 120", m["intervalSeconds"])
	}
}

func TestE2E_AutoUpdate_IntervalFloor(t *testing.T) {
	base, _ := startWebServer(t)
	createDefaultProfile(t, base)

	// Anything <60s gets bumped to the 60s floor; 0 stays 0 (means
	// "follow the server's nextFetch cadence with the built-in default").
	resp := postJSON(t, base+"/api/auto-update", `{"channels":["x"],"intervalSeconds":5}`)
	m := decodeJSON(t, resp)
	if iv, _ := m["intervalSeconds"].(float64); int(iv) != 60 {
		t.Errorf("intervalSeconds floor = %v, want 60", m["intervalSeconds"])
	}

	resp2 := postJSON(t, base+"/api/auto-update", `{"channels":["x"],"intervalSeconds":0}`)
	m2 := decodeJSON(t, resp2)
	if iv, _ := m2["intervalSeconds"].(float64); int(iv) != 0 {
		t.Errorf("intervalSeconds zero = %v, want 0 (default)", m2["intervalSeconds"])
	}
}

func TestE2E_AutoUpdate_NoActiveProfile(t *testing.T) {
	base, _ := startWebServer(t)

	// No profile created → no active profile → POST should fail with 400.
	resp := postJSON(t, base+"/api/auto-update/toggle", `{"channel":"x"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("toggle without profile: expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// GET should succeed and return empty channels.
	resp2 := getJSON(t, base+"/api/auto-update")
	if resp2.StatusCode != 200 {
		t.Fatalf("GET without profile: expected 200, got %d", resp2.StatusCode)
	}
	m := decodeJSON(t, resp2)
	chans, _ := m["channels"].([]any)
	if len(chans) != 0 {
		t.Errorf("expected empty channels with no profile, got %v", chans)
	}
}

func TestE2E_AutoUpdate_PersistsAcrossGets(t *testing.T) {
	base, _ := startWebServer(t)
	createDefaultProfile(t, base)

	postJSON(t, base+"/api/auto-update", `{"channels":["alpha","beta"]}`).Body.Close()

	// Fresh GET should return the same list.
	m := decodeJSON(t, getJSON(t, base+"/api/auto-update"))
	chans := m["channels"].([]any)
	if len(chans) != 2 || chans[0] != "alpha" || chans[1] != "beta" {
		t.Errorf("channels persisted = %v, want [alpha beta]", chans)
	}
}

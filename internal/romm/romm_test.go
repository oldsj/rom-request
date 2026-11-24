package romm

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScanPlatforms(t *testing.T) {
	status := http.StatusAccepted
	var got scanRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/tasks/scan" || r.Header.Get("Authorization") != "Bearer rmm_token" {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
	}))
	defer ts.Close()

	c, err := New(ts.URL+"/", "rmm_token", ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ScanPlatforms(t.Context(), []string{"GBA", "GENESIS"}); err != nil {
		t.Fatal(err)
	}
	if got.Type != "quick" || len(got.PlatformFolders) != 2 || got.PlatformFolders[1] != "GENESIS" {
		t.Errorf("body = %+v", got)
	}
	status = http.StatusConflict
	if err := c.ScanPlatforms(t.Context(), []string{"GBA"}); !errors.Is(err, ErrBusy) {
		t.Errorf("409 = %v, want ErrBusy", err)
	}
	status = http.StatusUnauthorized
	if err := c.ScanPlatforms(t.Context(), []string{"GBA"}); err == nil || errors.Is(err, ErrBusy) {
		t.Errorf("401 = %v", err)
	}
}

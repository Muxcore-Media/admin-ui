package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHttpSpoolAPIListSpools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/spools" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"spools": []map[string]any{{"url": "https://spool.test/main", "active": true}},
		})
	}))
	defer srv.Close()

	api := &httpSpoolAPI{base: srv.URL}
	spools, err := api.ListSpools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(spools) != 1 || spools[0].GetUrl() != "https://spool.test/main" {
		t.Fatalf("spools=%v", spools)
	}
}

func TestHttpSpoolAPIListTags(t *testing.T) {
	spoolURL := "https://spool.test/main"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/spools/"+spoolURL+"/tags" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tags": []map[string]any{{"name": "stable", "checksum": "abc"}},
		})
	}))
	defer srv.Close()

	api := &httpSpoolAPI{base: srv.URL}
	tags, err := api.ListTags(context.Background(), spoolURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].GetName() != "stable" {
		t.Fatalf("tags=%v", tags)
	}
}

func TestHttpSpoolAPIDeployTag(t *testing.T) {
	spoolURL := "https://spool.test/main"
	tagName := "stable"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/api/v1/spools/" + spoolURL + "/tags/" + tagName + "/deploy"
		if r.URL.Path != want || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total":   3,
			"spawned": 2,
			"skipped": 1,
			"failed":  0,
		})
	}))
	defer srv.Close()

	api := &httpSpoolAPI{base: srv.URL}
	resp, err := api.DeployTag(context.Background(), spoolURL, tagName)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetSpawned() != 2 || resp.GetSkipped() != 1 || resp.GetFailed() != 0 {
		t.Fatalf("resp=%v", resp)
	}
}

func TestHttpSpoolAPIFetchTag(t *testing.T) {
	spoolURL := "https://spool.test/main"
	tagName := "stable"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "/api/v1/spools/" + spoolURL + "/tags/" + tagName
		if r.URL.Path != want {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":        tagName,
			"description": "stable channel",
		})
	}))
	defer srv.Close()

	api := &httpSpoolAPI{base: srv.URL}
	resp, err := api.FetchTag(context.Background(), spoolURL, tagName)
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetName() != tagName {
		t.Fatalf("resp=%v", resp)
	}
}

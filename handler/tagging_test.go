package handler

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/admin-ui/session"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
)

func TestTaggingPageSoftWhenCoreMissing(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)

	r := mustRequest("GET", "/tagging")
	w := httptest.NewRecorder()
	h.TaggingPage(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 soft-empty page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="tagging-page"`) {
		t.Fatal("expected tagging page markup")
	}
	if !strings.Contains(body, `data-testid="tagging-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 400))
	}
}

type taggingMeshStub struct {
	meshv1.UnimplementedModuleMeshServer
	tags  []map[string]any
	rules []map[string]any
}

func (s *taggingMeshStub) Call(_ context.Context, req *meshv1.CallRequest) (*meshv1.CallResponse, error) {
	switch req.GetMethod() {
	case "ListTags":
		raw, _ := json.Marshal(s.tags)
		return &meshv1.CallResponse{Payload: raw}, nil
	case "ListRules":
		raw, _ := json.Marshal(s.rules)
		return &meshv1.CallResponse{Payload: raw}, nil
	case "CreateTag":
		var body map[string]string
		_ = json.Unmarshal(req.GetPayload(), &body)
		tag := map[string]any{"id": "tg_1", "name": body["name"], "category": body["category"], "color": body["color"]}
		s.tags = append(s.tags, tag)
		raw, _ := json.Marshal(tag)
		return &meshv1.CallResponse{Payload: raw}, nil
	case "UpsertRule":
		var body map[string]any
		_ = json.Unmarshal(req.GetPayload(), &body)
		rule := map[string]any{
			"id": "ru_1", "tag_id": body["tag_id"], "field": body["field"],
			"match": body["match"], "pattern": body["pattern"], "enabled": true,
		}
		s.rules = append(s.rules, rule)
		raw, _ := json.Marshal(rule)
		return &meshv1.CallResponse{Payload: raw}, nil
	default:
		return &meshv1.CallResponse{Error: "unknown method"}, nil
	}
}

func TestTaggingPageListsViaMesh(t *testing.T) {
	tagLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stub := &taggingMeshStub{
		tags: []map[string]any{
			{"id": "tg_anime", "name": "anime", "category": "genre", "color": "#f0f"},
		},
		rules: []map[string]any{
			{"id": "ru_1", "tag_id": "tg_anime", "field": "title", "match": "contains", "pattern": "naruto", "enabled": true},
		},
	}
	tagSrv := grpc.NewServer()
	meshv1.RegisterModuleMeshServer(tagSrv, stub)
	go func() { _ = tagSrv.Serve(tagLis) }()
	t.Cleanup(func() {
		tagSrv.Stop()
		_ = tagLis.Close()
	})

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, fixedDiscovery{
		mod: &discoveryv1.ModuleInfoProto{
			Id:           "media-tagging",
			Name:         "Content Tagging",
			HttpAddr:     tagLis.Addr().String(),
			Capabilities: []string{"media.tagging"},
		},
	})
	go func() { _ = discSrv.Serve(discLis) }()
	t.Cleanup(func() {
		discSrv.Stop()
		_ = discLis.Close()
	})

	core, err := client.Dial(discLis.Addr().String(), client.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	h.Core = core

	r := mustRequest("GET", "/tagging")
	w := httptest.NewRecorder()
	h.TaggingPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, truncate(w.Body.String(), 400))
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="tagging-rule-table"`) {
		t.Fatalf("expected rule table, got: %s", truncate(body, 600))
	}
	if !strings.Contains(body, "naruto") || !strings.Contains(body, "anime") {
		t.Fatalf("expected fixture rule/tag text, got: %s", truncate(body, 600))
	}
}

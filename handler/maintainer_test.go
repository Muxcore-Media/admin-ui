package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Muxcore-Media/admin-ui/session"
	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	"github.com/Muxcore-Media/core/sdk/go/client"
	maintainv1 "github.com/Muxcore-Media/media-library-maintainer/proto/maintainv1"
	"google.golang.org/grpc"
)

type maintainerStub struct {
	maintainv1.UnimplementedMaintainerServiceServer
	rules        []*maintainv1.RuleGroup
	candidates   []*maintainv1.Candidate
	scanDryRun   bool
	scanCalls    int
	approvedIDs  []string
	cancelledIDs []string
}

func (s *maintainerStub) ListRules(context.Context, *maintainv1.ListRulesRequest) (*maintainv1.ListRulesResponse, error) {
	return &maintainv1.ListRulesResponse{Rules: s.rules}, nil
}

func (s *maintainerStub) GetRule(_ context.Context, req *maintainv1.GetRuleRequest) (*maintainv1.GetRuleResponse, error) {
	for _, rule := range s.rules {
		if rule.GetId() == req.GetId() {
			return &maintainv1.GetRuleResponse{Rule: rule}, nil
		}
	}
	return &maintainv1.GetRuleResponse{}, nil
}

func (s *maintainerStub) UpsertRule(_ context.Context, req *maintainv1.UpsertRuleRequest) (*maintainv1.UpsertRuleResponse, error) {
	rule := req.GetRule()
	if rule.GetId() == "" {
		rule.Id = "rule_new"
		s.rules = append(s.rules, rule)
		return &maintainv1.UpsertRuleResponse{Rule: rule}, nil
	}
	for i, existing := range s.rules {
		if existing.GetId() == rule.GetId() {
			s.rules[i] = rule
			return &maintainv1.UpsertRuleResponse{Rule: rule}, nil
		}
	}
	s.rules = append(s.rules, rule)
	return &maintainv1.UpsertRuleResponse{Rule: rule}, nil
}

func (s *maintainerStub) DeleteRule(_ context.Context, req *maintainv1.DeleteRuleRequest) (*maintainv1.DeleteRuleResponse, error) {
	out := s.rules[:0]
	for _, rule := range s.rules {
		if rule.GetId() != req.GetId() {
			out = append(out, rule)
		}
	}
	s.rules = out
	return &maintainv1.DeleteRuleResponse{}, nil
}

func (s *maintainerStub) ListCandidates(context.Context, *maintainv1.ListCandidatesRequest) (*maintainv1.ListCandidatesResponse, error) {
	return &maintainv1.ListCandidatesResponse{Candidates: s.candidates}, nil
}

func (s *maintainerStub) ApproveCandidate(_ context.Context, req *maintainv1.ApproveCandidateRequest) (*maintainv1.ApproveCandidateResponse, error) {
	s.approvedIDs = append(s.approvedIDs, req.GetId())
	return &maintainv1.ApproveCandidateResponse{}, nil
}

func (s *maintainerStub) CancelCandidate(_ context.Context, req *maintainv1.CancelCandidateRequest) (*maintainv1.CancelCandidateResponse, error) {
	s.cancelledIDs = append(s.cancelledIDs, req.GetId())
	return &maintainv1.CancelCandidateResponse{}, nil
}

func (s *maintainerStub) ScanNow(_ context.Context, req *maintainv1.ScanNowRequest) (*maintainv1.ScanNowResponse, error) {
	s.scanCalls++
	s.scanDryRun = req.GetDryRun()
	return &maintainv1.ScanNowResponse{CandidatesFound: 2}, nil
}

func (s *maintainerStub) ListCollections(context.Context, *maintainv1.ListCollectionsRequest) (*maintainv1.ListCollectionsResponse, error) {
	return &maintainv1.ListCollectionsResponse{}, nil
}

func (s *maintainerStub) ListProtections(context.Context, *maintainv1.ListProtectionsRequest) (*maintainv1.ListProtectionsResponse, error) {
	return &maintainv1.ListProtectionsResponse{}, nil
}

func (s *maintainerStub) ListRuns(context.Context, *maintainv1.ListRunsRequest) (*maintainv1.ListRunsResponse, error) {
	return &maintainv1.ListRunsResponse{}, nil
}

func (s *maintainerStub) ListExclusionLists(context.Context, *maintainv1.ListExclusionListsRequest) (*maintainv1.ListExclusionListsResponse, error) {
	return &maintainv1.ListExclusionListsResponse{}, nil
}

func (s *maintainerStub) GetStorageMetrics(context.Context, *maintainv1.GetStorageMetricsRequest) (*maintainv1.GetStorageMetricsResponse, error) {
	return &maintainv1.GetStorageMetricsResponse{}, nil
}

func (s *maintainerStub) ListPlaybackUsers(context.Context, *maintainv1.ListPlaybackUsersRequest) (*maintainv1.ListPlaybackUsersResponse, error) {
	return &maintainv1.ListPlaybackUsersResponse{}, nil
}

func setupMaintainerHandler(t *testing.T, stub *maintainerStub) *Handler {
	t.Helper()
	mLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mSrv := grpc.NewServer()
	maintainv1.RegisterMaintainerServiceServer(mSrv, stub)
	go func() { _ = mSrv.Serve(mLis) }()
	t.Cleanup(func() {
		mSrv.Stop()
		_ = mLis.Close()
	})

	discLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	discSrv := grpc.NewServer()
	discoveryv1.RegisterDiscoveryServiceServer(discSrv, capDiscovery{
		mods: map[string]*discoveryv1.ModuleInfoProto{
			capMediaLibraryMaintainer: {Id: "media-library-maintainer", HttpAddr: mLis.Addr().String()},
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
	h := New(nil, ss, false, "test", nil, true, "", nil, nil)
	h.Core = core
	return h
}

func TestMaintainerPageSoftEmptyWithoutCore(t *testing.T) {
	ss := session.NewStore(0)
	h := New(nil, ss, false, "test", nil, false, "", nil, nil)
	r := mustRequest(http.MethodGet, "/maintainer")
	w := httptest.NewRecorder()
	h.MaintainerPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-testid="maintainer-page"`) {
		t.Fatal("expected maintainer page")
	}
	if !strings.Contains(body, `data-testid="maintainer-soft-empty"`) {
		t.Fatalf("expected soft-empty note, got: %s", truncate(body, 600))
	}
}

func TestMaintainerPageListsRules(t *testing.T) {
	stub := &maintainerStub{
		rules: []*maintainv1.RuleGroup{{
			Id:        "rule_1",
			Name:      "Stale movies",
			Enabled:   true,
			Scope:     maintainv1.MediaScope_MEDIA_SCOPE_MOVIE,
			Outcome:   maintainv1.RuleOutcome_RULE_OUTCOME_CANDIDATE,
			ArrAction: maintainv1.ArrAction_ARR_ACTION_DELETE,
		}},
	}
	h := setupMaintainerHandler(t, stub)

	r := mustRequest(http.MethodGet, "/maintainer")
	w := httptest.NewRecorder()
	h.MaintainerPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Stale movies") {
		t.Fatalf("expected rule name in page, got: %s", truncate(body, 600))
	}
	if strings.Contains(body, `data-testid="maintainer-soft-empty"`) {
		t.Fatal("did not expect soft-empty when module is available")
	}
}

func TestMaintainerDryRunScan(t *testing.T) {
	stub := &maintainerStub{}
	h := setupMaintainerHandler(t, stub)

	body := strings.NewReader("dry_run=true")
	r := httptest.NewRequest(http.MethodPost, "/maintainer/scan", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.MaintainerScan(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if stub.scanCalls != 1 || !stub.scanDryRun {
		t.Fatalf("scanCalls=%d dryRun=%v", stub.scanCalls, stub.scanDryRun)
	}
}

func TestMaintainerToggleRule(t *testing.T) {
	stub := &maintainerStub{
		rules: []*maintainv1.RuleGroup{{Id: "rule_1", Name: "x", Enabled: true}},
	}
	h := setupMaintainerHandler(t, stub)

	r := httptest.NewRequest(http.MethodPost, "/maintainer/rules/rule_1/toggle", nil)
	r.SetPathValue("id", "rule_1")
	w := httptest.NewRecorder()
	h.MaintainerToggleRule(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if len(stub.rules) != 1 || stub.rules[0].GetEnabled() {
		t.Fatalf("expected disabled rule, got %+v", stub.rules)
	}
}

func TestMaintainerApproveCandidate(t *testing.T) {
	stub := &maintainerStub{}
	h := setupMaintainerHandler(t, stub)

	r := httptest.NewRequest(http.MethodPost, "/maintainer/candidates/cand_1/approve", nil)
	r.SetPathValue("id", "cand_1")
	w := httptest.NewRecorder()
	h.MaintainerApproveCandidate(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if len(stub.approvedIDs) != 1 || stub.approvedIDs[0] != "cand_1" {
		t.Fatalf("approvedIDs=%v", stub.approvedIDs)
	}
}

func TestMaintainerCancelCandidate(t *testing.T) {
	stub := &maintainerStub{}
	h := setupMaintainerHandler(t, stub)

	r := httptest.NewRequest(http.MethodPost, "/maintainer/candidates/cand_2/cancel", nil)
	r.SetPathValue("id", "cand_2")
	w := httptest.NewRecorder()
	h.MaintainerCancelCandidate(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	if len(stub.cancelledIDs) != 1 || stub.cancelledIDs[0] != "cand_2" {
		t.Fatalf("cancelledIDs=%v", stub.cancelledIDs)
	}
}

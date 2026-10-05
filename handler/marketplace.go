package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Muxcore-Media/admin-ui/internal/meshdial"

	spoolv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/spool/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	marketplaceDialTimeout = 3 * time.Second
	marketplaceReadTimeout = 8 * time.Second
	marketplacePageTimeout = marketplaceDialTimeout + 3*marketplaceReadTimeout + time.Second
)

// setUserBearer forwards the signed-in user's auth-local token to api-rest.
func setUserBearer(ctx context.Context, req *http.Request) {
	if sess := SessionFromContext(ctx); sess != nil && strings.TrimSpace(sess.AuthLocalToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(sess.AuthLocalToken))
	}
}

func marketplaceHTTPDo(_ context.Context, req *http.Request) (*http.Response, error) {
	return (&http.Client{Timeout: marketplaceReadTimeout}).Do(req)
}

// SpoolAPI is the marketplace DeployTag surface (gRPC or HTTP stub).
type SpoolAPI interface {
	ListSpools(ctx context.Context) ([]*spoolv1.SpoolInfo, error)
	ListTags(ctx context.Context, spoolURL string) ([]*spoolv1.TagSummary, error)
	FetchTag(ctx context.Context, spoolURL, tagName string) (*spoolv1.FetchTagResponse, error)
	DeployTag(ctx context.Context, spoolURL, tagName string) (*spoolv1.DeployTagResponse, error)
}

type grpcSpoolAPI struct {
	client spoolv1.SpoolServiceClient
}

func (g *grpcSpoolAPI) ListSpools(ctx context.Context) ([]*spoolv1.SpoolInfo, error) {
	resp, err := g.client.ListSpools(withUserAuth(ctx), &spoolv1.ListSpoolsRequest{})
	if err != nil {
		return nil, err
	}
	return resp.GetSpools(), nil
}

func (g *grpcSpoolAPI) ListTags(ctx context.Context, spoolURL string) ([]*spoolv1.TagSummary, error) {
	resp, err := g.client.ListTags(withUserAuth(ctx), &spoolv1.ListTagsRequest{SpoolUrl: spoolURL})
	if err != nil {
		return nil, err
	}
	return resp.GetTags(), nil
}

func (g *grpcSpoolAPI) FetchTag(ctx context.Context, spoolURL, tagName string) (*spoolv1.FetchTagResponse, error) {
	return g.client.FetchTag(withUserAuth(ctx), &spoolv1.FetchTagRequest{SpoolUrl: spoolURL, TagName: tagName})
}

func (g *grpcSpoolAPI) DeployTag(ctx context.Context, spoolURL, tagName string) (*spoolv1.DeployTagResponse, error) {
	return g.client.DeployTag(withUserAuth(ctx), &spoolv1.DeployTagRequest{SpoolUrl: spoolURL, TagName: tagName})
}

// httpSpoolAPI proxies through api-rest HTTP routes.
type httpSpoolAPI struct {
	base string
}

func (h *httpSpoolAPI) ListSpools(ctx context.Context) ([]*spoolv1.SpoolInfo, error) {
	var out struct {
		Spools []*spoolv1.SpoolInfo `json:"spools"`
	}
	if err := h.get(ctx, "/api/v1/spools", &out); err != nil {
		return nil, err
	}
	return out.Spools, nil
}

func (h *httpSpoolAPI) ListTags(ctx context.Context, spoolURL string) ([]*spoolv1.TagSummary, error) {
	var out struct {
		Tags []*spoolv1.TagSummary `json:"tags"`
	}
	path := "/api/v1/spools/" + url.PathEscape(spoolURL) + "/tags"
	if err := h.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return out.Tags, nil
}

func (h *httpSpoolAPI) FetchTag(ctx context.Context, spoolURL, tagName string) (*spoolv1.FetchTagResponse, error) {
	path := "/api/v1/spools/" + url.PathEscape(spoolURL) + "/tags/" + url.PathEscape(tagName)
	var out spoolv1.FetchTagResponse
	if err := h.get(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *httpSpoolAPI) DeployTag(ctx context.Context, spoolURL, tagName string) (*spoolv1.DeployTagResponse, error) {
	path := "/api/v1/spools/" + url.PathEscape(spoolURL) + "/tags/" + url.PathEscape(tagName) + "/deploy"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.base, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	setUserBearer(ctx, req)
	resp, err := marketplaceHTTPDo(ctx, req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("api-rest deploy: %s", strings.TrimSpace(string(body)))
	}
	var out spoolv1.DeployTagResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *httpSpoolAPI) get(ctx context.Context, path string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(h.base, "/")+path, nil)
	if err != nil {
		return err
	}
	setUserBearer(ctx, req)
	resp, err := marketplaceHTTPDo(ctx, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api-rest %s: %s", path, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

func (h *Handler) initSpoolAPI() {
	if h.Spool != nil {
		return
	}
	if h.Core != nil && h.coreConnected {
		conn, err := meshdial.NewClient(h.Core.CurrentAddr())
		if err == nil {
			h.Spool = &grpcSpoolAPI{client: spoolv1.NewSpoolServiceClient(conn)}
			return
		}
		slog.Warn("marketplace: spool gRPC dial failed", "error", err)
	}
	if base := strings.TrimSpace(os.Getenv("ADMIN_UI_API_REST_URL")); base != "" {
		h.Spool = &httpSpoolAPI{base: base}
		h.APIRestURL = base
	}
}

func (h *Handler) MarketplacePage(w http.ResponseWriter, r *http.Request) {
	h.initSpoolAPI()
	pageCtx, cancel := context.WithTimeout(r.Context(), marketplacePageTimeout)
	defer cancel()

	data := templates.MarketplacePageData{APIRestHint: h.APIRestURL != "" || h.Spool == nil}
	if msg := r.URL.Query().Get("deployed"); msg != "" {
		data.DeployResult = msg
	}
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		data.Error = errMsg
	}
	if h.Spool == nil {
		data.Error = "SpoolService unavailable — connect admin-ui to core (gRPC) or set ADMIN_UI_API_REST_URL (api-rest HTTP spool routes). DeployTag is not available without one of these."
		data.SpoolHint = "api-rest exposes GET/POST /api/v1/spools/{url}/tags/{name}[/deploy]; without ADMIN_UI_API_REST_URL the UI uses core gRPC only."
	} else {
		spools, err := h.Spool.ListSpools(pageCtx)
		if err != nil {
			data.Error = err.Error()
		} else {
			for _, sp := range spools {
				entry := templates.MarketplaceSpool{URL: sp.GetUrl(), Active: sp.GetActive()}
				tags, terr := h.Spool.ListTags(pageCtx, sp.GetUrl())
				if terr != nil {
					slog.Warn("marketplace: ListTags", "spool", sp.GetUrl(), "error", terr)
				} else {
					for _, tag := range tags {
						mt := templates.MarketplaceTag{
							Name:        tag.GetName(),
							Description: tag.GetDescription(),
							Version:     tag.GetVersion(),
							ModuleCount: int(tag.GetModuleCount()),
						}
						if detail, ferr := h.Spool.FetchTag(pageCtx, sp.GetUrl(), tag.GetName()); ferr != nil {
							slog.Warn("marketplace: FetchTag", "spool", sp.GetUrl(), "tag", tag.GetName(), "error", ferr)
						} else {
							for _, mod := range detail.GetModules() {
								mt.Modules = append(mt.Modules, templates.MarketplaceModulePin{
									Repo:      mod.GetRepo(),
									Version:   mod.GetVersion(),
									Checksum:  mod.GetChecksum(),
									Publisher: mod.GetPublisher(),
									Required:  mod.GetRequired(),
								})
							}
							if mt.ModuleCount == 0 {
								mt.ModuleCount = len(mt.Modules)
							}
						}
						entry.Tags = append(entry.Tags, mt)
					}
				}
				data.Spools = append(data.Spools, entry)
			}
		}
	}
	content := templates.MarketplacePage(data)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Marketplace", nav, content))
}

func (h *Handler) MarketplaceDeploy(w http.ResponseWriter, r *http.Request) {
	h.initSpoolAPI()
	ctx, cancel := context.WithTimeout(r.Context(), marketplaceDialTimeout+marketplaceReadTimeout)
	defer cancel()
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/marketplace?error="+url.QueryEscape("invalid form"), http.StatusSeeOther)
		return
	}
	spoolURL := strings.TrimSpace(r.FormValue("spool_url"))
	tagName := strings.TrimSpace(r.FormValue("tag_name"))
	if spoolURL == "" || tagName == "" {
		http.Redirect(w, r, "/marketplace?error="+url.QueryEscape("spool_url and tag_name required"), http.StatusSeeOther)
		return
	}
	if h.Spool == nil {
		http.Redirect(w, r, "/marketplace?error="+url.QueryEscape("SpoolService unavailable — use core gRPC or ADMIN_UI_API_REST_URL"), http.StatusSeeOther)
		return
	}
	resp, err := h.Spool.DeployTag(ctx, spoolURL, tagName)
	if err != nil {
		http.Redirect(w, r, "/marketplace?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	msg := fmt.Sprintf("Deployed %s (checksums verified by DeployTag): total=%d spawned=%d skipped=%d failed=%d",
		resp.GetTagName(), resp.GetTotal(), resp.GetSpawned(), resp.GetSkipped(), resp.GetFailed())
	for _, res := range resp.GetResults() {
		if res.GetError() != "" {
			msg += fmt.Sprintf("\n- %s: %s", res.GetModuleId(), res.GetError())
		} else if res.GetSpawned() {
			msg += fmt.Sprintf("\n- %s: spawned", res.GetModuleId())
		} else if res.GetAlreadyRunning() {
			msg += fmt.Sprintf("\n- %s: already running", res.GetModuleId())
		}
	}
	actor := "admin"
	if sess := SessionFromContext(r.Context()); sess != nil && sess.UserID != "" {
		actor = sess.UserID
	}
	h.auditLog(r.Context(), actor, "marketplace.deploy", "spool", tagName, map[string]string{
		"spool_url":         spoolURL,
		"tag":               tagName,
		"checksum_verified": "true",
		"spawned":           fmt.Sprintf("%d", resp.GetSpawned()),
		"failed":            fmt.Sprintf("%d", resp.GetFailed()),
	})
	http.Redirect(w, r, "/marketplace?deployed="+url.QueryEscape(msg), http.StatusSeeOther)
}

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const (
	capMediaMusic     = "media.music"
	musicMeshList     = "ListArtists"
	musicMeshGet      = "GetArtist"
	musicPageTimeout  = 8 * time.Second
	musicHTTPPathList = "/api/artists"
)

type musicArtistJSON struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	MusicBrainzID string `json:"musicbrainz_id"`
	Monitored     bool   `json:"monitored"`
	Path          string `json:"path"`
}

type musicAlbumJSON struct {
	ID            string `json:"id"`
	ArtistID      string `json:"artist_id"`
	Title         string `json:"title"`
	MusicBrainzID string `json:"musicbrainz_id"`
	Year          int32  `json:"year"`
	Monitored     bool   `json:"monitored"`
}

type musicDetailJSON struct {
	Artist musicArtistJSON  `json:"artist"`
	Albums []musicAlbumJSON `json:"albums"`
}

func (h *Handler) musicModule(ctx context.Context) (id, grpcAddr, name string, err error) {
	if h.Core == nil {
		return "", "", "", fmt.Errorf("core unavailable")
	}
	mods, err := h.Core.Discovery.FindByCapability(ctx, capMediaMusic)
	if err != nil {
		return "", "", "", err
	}
	if len(mods) == 0 {
		return "", "", "", fmt.Errorf("no module with capability %s", capMediaMusic)
	}
	mod := mods[0]
	addr := normalizeDialAddr(mod.GetId(), mod.GetHttpAddr())
	if addr == "" {
		return "", "", "", fmt.Errorf("music module has no dial address")
	}
	return mod.GetId(), addr, mod.GetName(), nil
}

// musicMeshCall mirrors settingsMeshCall: direct ModuleMesh dial, then core mesh fallback.
func (h *Handler) musicMeshCall(ctx context.Context, moduleID, httpAddr, method string, payload []byte) ([]byte, error) {
	if httpAddr != "" {
		conn, err := grpc.NewClient(httpAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			defer conn.Close()
			client := meshv1.NewModuleMeshClient(conn)
			resp, err := client.Call(ctx, &meshv1.CallRequest{
				TargetModule: moduleID,
				Method:       method,
				Payload:      payload,
			})
			if err == nil && resp.GetError() == "" {
				return resp.GetPayload(), nil
			}
			if err != nil {
				slog.Debug("music: direct mesh dial failed, trying core mesh", "module", moduleID, "error", err)
			} else if resp.GetError() != "" {
				slog.Debug("music: direct mesh error, trying core mesh", "module", moduleID, "error", resp.GetError())
			}
		}
	}
	if h.Core == nil || h.Core.Mesh == nil {
		return nil, fmt.Errorf("mesh unavailable")
	}
	return h.Core.Mesh.Call(ctx, moduleID, method, payload)
}

// musicHTTPBaseFromGRPC maps media-music discovery gRPC addr to health HTTP (grpcPort+1).
func musicHTTPBaseFromGRPC(grpcAddr string) string {
	host, portStr, err := net.SplitHostPort(grpcAddr)
	if err != nil {
		return ""
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port+1))
}

func (h *Handler) musicFetchArtists(ctx context.Context, moduleID, grpcAddr, query string) ([]templates.MusicArtistRow, error) {
	payload, _ := json.Marshal(map[string]string{"query": query})
	raw, err := h.musicMeshCall(ctx, moduleID, grpcAddr, musicMeshList, payload)
	if err == nil {
		return decodeArtistRows(raw)
	}
	slog.Debug("music: mesh ListArtists failed, trying HTTP stub", "error", err)

	base := musicHTTPBaseFromGRPC(grpcAddr)
	if base == "" {
		return nil, err
	}
	u, uerr := url.Parse(base + musicHTTPPathList)
	if uerr != nil {
		return nil, err
	}
	if query != "" {
		q := u.Query()
		q.Set("q", query)
		u.RawQuery = q.Encode()
	}
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if reqErr != nil {
		return nil, err
	}
	resp, httpErr := http.DefaultClient.Do(req)
	if httpErr != nil {
		return nil, fmt.Errorf("mesh: %v; http: %w", err, httpErr)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mesh: %v; http status %d", err, resp.StatusCode)
	}
	return decodeArtistRows(body)
}

func (h *Handler) musicFetchArtist(ctx context.Context, moduleID, grpcAddr, id string) (templates.MusicArtistRow, []templates.MusicAlbumRow, error) {
	payload, _ := json.Marshal(map[string]string{"id": id})
	raw, err := h.musicMeshCall(ctx, moduleID, grpcAddr, musicMeshGet, payload)
	if err == nil {
		return decodeArtistDetail(raw)
	}
	slog.Debug("music: mesh GetArtist failed, trying HTTP stub", "error", err)

	base := musicHTTPBaseFromGRPC(grpcAddr)
	if base == "" {
		return templates.MusicArtistRow{}, nil, err
	}
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, base+musicHTTPPathList+"/"+id, nil)
	if reqErr != nil {
		return templates.MusicArtistRow{}, nil, err
	}
	resp, httpErr := http.DefaultClient.Do(req)
	if httpErr != nil {
		return templates.MusicArtistRow{}, nil, fmt.Errorf("mesh: %v; http: %w", err, httpErr)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return templates.MusicArtistRow{}, nil, fmt.Errorf("mesh: %v; http status %d", err, resp.StatusCode)
	}
	return decodeArtistDetail(body)
}

func decodeArtistRows(raw []byte) ([]templates.MusicArtistRow, error) {
	var items []musicArtistJSON
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	out := make([]templates.MusicArtistRow, 0, len(items))
	for _, a := range items {
		out = append(out, templates.MusicArtistRow{
			ID: a.ID, Name: a.Name, MusicBrainzID: a.MusicBrainzID,
			Monitored: a.Monitored, Path: a.Path,
		})
	}
	return out, nil
}

func decodeArtistDetail(raw []byte) (templates.MusicArtistRow, []templates.MusicAlbumRow, error) {
	var detail musicDetailJSON
	if err := json.Unmarshal(raw, &detail); err != nil {
		return templates.MusicArtistRow{}, nil, err
	}
	artist := templates.MusicArtistRow{
		ID: detail.Artist.ID, Name: detail.Artist.Name, MusicBrainzID: detail.Artist.MusicBrainzID,
		Monitored: detail.Artist.Monitored, Path: detail.Artist.Path,
	}
	albums := make([]templates.MusicAlbumRow, 0, len(detail.Albums))
	for _, al := range detail.Albums {
		albums = append(albums, templates.MusicAlbumRow{
			ID: al.ID, ArtistID: al.ArtistID, Title: al.Title,
			MusicBrainzID: al.MusicBrainzID, Year: int(al.Year), Monitored: al.Monitored,
		})
	}
	return artist, albums, nil
}

func (h *Handler) MusicListPage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), musicPageTimeout)
	defer cancel()

	query := r.URL.Query().Get("q")
	data := templates.MusicPageData{Query: query}

	moduleID, addr, name, err := h.musicModule(ctx)
	if err != nil {
		slog.Warn("music: resolve failed", "error", err)
		data.Error = err.Error()
		data.SoftEmpty = true
		h.renderMusicList(w, r, data)
		return
	}
	data.ModuleID = moduleID
	data.ModuleName = name

	artists, err := h.musicFetchArtists(ctx, moduleID, addr, query)
	if err != nil {
		slog.Warn("music: list failed", "module", moduleID, "error", err)
		data.Error = err.Error()
		data.SoftEmpty = true
		h.renderMusicList(w, r, data)
		return
	}
	data.Artists = artists
	h.renderMusicList(w, r, data)
}

func (h *Handler) MusicDetailPage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), musicPageTimeout)
	defer cancel()

	id := r.PathValue("id")
	data := templates.MusicDetailData{}

	moduleID, addr, name, err := h.musicModule(ctx)
	if err != nil {
		slog.Warn("music: resolve failed", "error", err)
		data.Error = err.Error()
		data.Artist = templates.MusicArtistRow{ID: id, Name: id}
		h.renderMusicDetail(w, r, data)
		return
	}
	data.ModuleID = moduleID
	data.ModuleName = name

	artist, albums, err := h.musicFetchArtist(ctx, moduleID, addr, id)
	if err != nil {
		slog.Warn("music: detail failed", "module", moduleID, "id", id, "error", err)
		data.Error = err.Error()
		data.Artist = templates.MusicArtistRow{ID: id, Name: id}
		h.renderMusicDetail(w, r, data)
		return
	}
	data.Artist = artist
	data.Albums = albums
	h.renderMusicDetail(w, r, data)
}

func (h *Handler) renderMusicList(w http.ResponseWriter, r *http.Request, data templates.MusicPageData) {
	content := templates.MusicListPage(data)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout("Music", nav, content))
}

func (h *Handler) renderMusicDetail(w http.ResponseWriter, r *http.Request, data templates.MusicDetailData) {
	title := data.Artist.Name
	if title == "" {
		title = "Music"
	}
	content := templates.MusicDetailPage(data)
	nav := h.nav(r.URL.Path)
	h.render(w, r, templates.Layout(title+" — Music", nav, content))
}

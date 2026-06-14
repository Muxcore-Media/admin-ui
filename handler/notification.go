package handler

import (
	"log/slog"
	"net/http"

	"google.golang.org/grpc"

	notifyv1 "github.com/Muxcore-Media/notification-default/proto/notifyv1"

	templates "github.com/Muxcore-Media/admin-ui/templ"
)

const capNotification = "notification"

func (h *Handler) dialNotification(ctx *http.Request) (*grpc.ClientConn, notifyv1.NotificationServiceClient, error) {
	mod, err := h.Core.Discovery.Resolve(ctx.Context(), "notification-default")
	if err != nil {
		mods, err2 := h.Core.Discovery.FindByCapability(ctx.Context(), capNotification)
		if err2 != nil || len(mods) == 0 {
			return nil, nil, err
		}
		conn, err3 := h.cachedConn(ctx.Context(), mods[0].GetHttpAddr())
		if err3 != nil {
			return nil, nil, err3
		}
		return conn, notifyv1.NewNotificationServiceClient(conn), nil
	}
	conn, err := h.cachedConn(ctx.Context(), mod.GetHttpAddr())
	if err != nil {
		return nil, nil, err
	}
	return conn, notifyv1.NewNotificationServiceClient(conn), nil
}

func toChannelStatus(proto []*notifyv1.ChannelStatus) []templates.ChannelStatus {
	items := make([]templates.ChannelStatus, 0, len(proto))
	for _, c := range proto {
		items = append(items, templates.ChannelStatus{
			Channel:     c.GetChannel().String(),
			Enabled:     c.GetEnabled(),
			Description: c.GetDescription(),
		})
	}
	return items
}

func (h *Handler) NotificationsPage(w http.ResponseWriter, r *http.Request) {
	_, client, err := h.dialNotification(r)
	if err != nil {
		slog.Warn("notification: dial failed", "error", err)
		content := templates.NotificationsPage(nil)
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Notifications", nav, content)
		h.render(w, r, component)
		return
	}

	resp, err := client.Status(r.Context(), &notifyv1.StatusRequest{})
	if err != nil {
		slog.Warn("notification: Status failed", "error", err)
		content := templates.NotificationsPage(nil)
		nav := h.nav(r.URL.Path)
		component := templates.Layout("Notifications", nav, content)
		h.render(w, r, component)
		return
	}

	channels := toChannelStatus(resp.GetChannels())
	content := templates.NotificationsPage(channels)
	nav := h.nav(r.URL.Path)
	component := templates.Layout("Notifications", nav, content)
	h.render(w, r, component)
}

func (h *Handler) NotificationConfigure(w http.ResponseWriter, r *http.Request) {
	channel := r.PathValue("channel")
	if err := r.ParseForm(); err != nil {
		toast(w, "error", "Invalid form data")
		return
	}

	_, client, err := h.dialNotification(r)
	if err != nil {
		toast(w, "error", "Notification module unavailable")
		return
	}

	settings := make(map[string]string)
	for k, v := range r.Form {
		if len(v) > 0 && v[0] != "" {
			settings[k] = v[0]
		}
	}

	channelEnum := parseChannelEnum(channel)
	_, err = client.Configure(r.Context(), &notifyv1.ConfigureRequest{
		Channel:  channelEnum,
		Settings: settings,
	})
	if err != nil {
		slog.Warn("notification: Configure failed", "channel", channel, "error", err)
		toast(w, "error", "Configure failed: "+err.Error())
		return
	}

	h.auditLog(r.Context(), SessionFromContext(r.Context()).Username, "notification.configure", "channel", channel, nil)
	toast(w, "success", "Channel configured")

	// Refresh the status panel
	resp, err := client.Status(r.Context(), &notifyv1.StatusRequest{})
	if err != nil {
		return
	}
	channels := toChannelStatus(resp.GetChannels())
	component := templates.NotificationsPage(channels)
	h.render(w, r, component)
}

func (h *Handler) NotificationTest(w http.ResponseWriter, r *http.Request) {
	channel := r.PathValue("channel")
	_, client, err := h.dialNotification(r)
	if err != nil {
		toast(w, "error", "Notification module unavailable")
		return
	}

	channelEnum := parseChannelEnum(channel)
	_, err = client.Notify(r.Context(), &notifyv1.NotifyRequest{
		Title:    "MuxCore Admin Test",
		Message:  "This is a test notification from the MuxCore admin UI.",
		Severity: "info",
		Channels: []notifyv1.Channel{channelEnum},
	})
	if err != nil {
		slog.Warn("notification: Notify failed", "channel", channel, "error", err)
		toast(w, "error", "Test failed: "+err.Error())
		return
	}

	toast(w, "success", "Test notification sent")
}

func parseChannelEnum(ch string) notifyv1.Channel {
	switch ch {
	case "discord":
		return notifyv1.Channel_CHANNEL_DISCORD
	case "slack":
		return notifyv1.Channel_CHANNEL_SLACK
	case "webhook":
		return notifyv1.Channel_CHANNEL_WEBHOOK
	case "email":
		return notifyv1.Channel_CHANNEL_EMAIL
	default:
		return notifyv1.Channel_CHANNEL_UNSPECIFIED
	}
}

// Package branding mirrors github.com/Muxcore-Media/branding for admin-ui chrome.
// tokens/theme.json is synced from that module; prefer importing the published
// module when available in the workspace.
package branding

import (
	_ "embed"
	"encoding/json"
)

//go:embed tokens/theme.json
var themeJSON []byte

const (
	ProductName  = "MuxCore"
	ShortName    = "MuxCore"
	ProductTitle = "MuxCore Admin"
	Tagline      = "Your household media hub."

	AssetLogoMark = "logo/logo-mark.svg"
	AssetLogo     = "logo/logo.svg"
)

// Theme holds CSS-friendly color and typography tokens shared across household UI.
type Theme struct {
	Colors     ColorTokens      `json:"colors"`
	Typography TypographyTokens `json:"typography"`
}

// ColorTokens are hex color values suitable for CSS custom properties.
type ColorTokens struct {
	Primary      string `json:"primary"`
	PrimaryHover string `json:"primaryHover"`
	Background   string `json:"background"`
	Surface      string `json:"surface"`
	Text         string `json:"text"`
	TextMuted    string `json:"textMuted"`
	Accent       string `json:"accent"`
	Border       string `json:"border"`
	Success      string `json:"success"`
	Warning      string `json:"warning"`
	Error        string `json:"error"`
}

// TypographyTokens hold font family and scale hints for clients.
type TypographyTokens struct {
	FontFamilySans string `json:"fontFamilySans"`
	FontFamilyMono string `json:"fontFamilyMono"`
	FontSizeBase   string `json:"fontSizeBase"`
	FontSizeSm     string `json:"fontSizeSm"`
	FontSizeLg     string `json:"fontSizeLg"`
}

type themeFile struct {
	Colors       ColorTokens       `json:"colors"`
	Typography   TypographyTokens  `json:"typography"`
	CSSVariables map[string]string `json:"cssVariables"`
}

// DefaultTheme returns the canonical MuxCore household theme tokens.
func DefaultTheme() Theme {
	var tf themeFile
	if err := json.Unmarshal(themeJSON, &tf); err != nil {
		return Theme{}
	}
	return Theme{
		Colors:     tf.Colors,
		Typography: tf.Typography,
	}
}

// CSSVariables returns a map of CSS custom property names to token values.
func (t Theme) CSSVariables() map[string]string {
	return map[string]string{
		"--muxcore-color-primary":       t.Colors.Primary,
		"--muxcore-color-primary-hover": t.Colors.PrimaryHover,
		"--muxcore-color-background":    t.Colors.Background,
		"--muxcore-color-surface":       t.Colors.Surface,
		"--muxcore-color-text":          t.Colors.Text,
		"--muxcore-color-text-muted":    t.Colors.TextMuted,
		"--muxcore-color-accent":        t.Colors.Accent,
		"--muxcore-color-border":        t.Colors.Border,
		"--muxcore-color-success":       t.Colors.Success,
		"--muxcore-color-warning":       t.Colors.Warning,
		"--muxcore-color-error":         t.Colors.Error,
		"--muxcore-font-family-sans":    t.Typography.FontFamilySans,
		"--muxcore-font-family-mono":    t.Typography.FontFamilyMono,
		"--muxcore-font-size-base":      t.Typography.FontSizeBase,
		"--muxcore-font-size-sm":        t.Typography.FontSizeSm,
		"--muxcore-font-size-lg":        t.Typography.FontSizeLg,
	}
}

// ThemeCSSVariables returns the cssVariables block from tokens/theme.json.
func ThemeCSSVariables() map[string]string {
	var tf themeFile
	if err := json.Unmarshal(themeJSON, &tf); err != nil {
		return nil
	}
	return tf.CSSVariables
}

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// telegramTextLimit is the text field limit (in characters) of Telegram
// sendMessage.
const telegramTextLimit = 4096

// telegramChannel implements the Telegram Bot API.
//
// Platform traits:
//   - Authentication lives entirely in the URL path (/bot<token>/sendMessage);
//     no signing needed.
//   - Uses HTML parse mode instead of MarkdownV2: MarkdownV2 requires escaping
//     18 characters `_*[]()~`>#+-=|{}.!` and missing a single one gets the whole
//     message rejected; HTML only requires escaping & < >.
//   - Business errors likewise hide inside HTTP 200, detected via the ok field.
type telegramChannel struct{}

func (telegramChannel) Kind() string { return KindTelegram }

// Telegram allows about 1 message/second in private chats and 20 messages/minute
// in groups. Take the conservative value.
func (telegramChannel) DefaultRatePerMin() int { return 20 }

// The Bot Token is the full credential; chat_id is just the recipient, not a
// secret (having it without the Token does not let you send messages).
func (telegramChannel) SecretKeys() []string { return []string{"bot_token"} }

// base_url decides which API endpoint the Token is sent to (e.g. a self-hosted
// reverse proxy); changing it requires re-declaring the Token.
func (telegramChannel) DestinationKeys() []string { return []string{"base_url"} }

// body_template is the escape hatch for the built-in layout: a Go template over
// the same data the webhook exposes, so a chat can be told exactly what to show
// (a one-line alert, a table, a deep link) without a second channel kind.
// parse_mode goes with it (plain/HTML/MarkdownV2); the built-in layout always
// sends HTML.
//
// Three destination options are Telegram-only and earn their place: chat_id is
// the chat, message_thread_id picks a forum topic inside it, silent suppresses
// the notification sound, and link_preview turns the web-page preview on (off
// by default, because a single finding message would otherwise load the target
// page as a preview card in every recipient's client).
func (telegramChannel) Validate(cfg map[string]any) error {
	if cfgString(cfg, "bot_token") == "" {
		return errors.New("missing Bot Token")
	}
	if cfgString(cfg, "chat_id") == "" {
		return errors.New("missing Chat ID")
	}
	if base := cfgString(cfg, "base_url"); base != "" {
		if err := validateHTTPURL(base); err != nil {
			return fmt.Errorf("invalid API URL: %w", err)
		}
	}
	if raw, present := cfg["message_thread_id"]; present && raw != nil {
		// cfgInt turns a non-numeric value into 0, which would silently send to
		// the General topic instead of the configured one. Reject the typo at
		// save time instead.
		if s, isStr := raw.(string); isStr && strings.TrimSpace(s) != "" && cfgInt(cfg, "message_thread_id") == 0 {
			return fmt.Errorf("message_thread_id must be a topic id (a positive integer), got %q", s)
		}
		if cfgInt(cfg, "message_thread_id") < 0 {
			return fmt.Errorf("message_thread_id must be a positive integer")
		}
	}
	if tpl := cfgString(cfg, "body_template"); tpl != "" {
		if _, err := parseWebhookTemplate(tpl); err != nil {
			return fmt.Errorf("body template syntax error: %w", err)
		}
	}
	return nil
}

func (c telegramChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	endpoint, err := telegramEndpoint(cfg)
	if err != nil {
		return 0, Permanent(err)
	}
	kept := len(m.Items)
	var text string
	if tpl := cfgString(cfg, "body_template"); tpl != "" {
		// A custom template owns the whole body, so truncation stops applying:
		// Telegram's length limit belongs to the built-in layout, and a template
		// that runs past it gets a plain API error the configurator can act on.
		body, rerr := renderWebhookBody(tpl, m)
		if rerr != nil {
			return 0, Permanent(rerr)
		}
		text = body
	} else {
		text, kept = telegramHTML(m)
	}
	payload := map[string]any{
		"chat_id":                  cfgString(cfg, "chat_id"),
		"text":                     text,
		"parse_mode":               telegramParseMode(cfg),
		"disable_web_page_preview": !cfgBool(cfg, "link_preview"),
	}
	// Forum supergroups deliver into a topic; without the thread id a message
	// lands in the General topic and is easily missed. Omitted otherwise, so a
	// plain group or private chat payload stays byte-identical.
	if thread := cfgInt(cfg, "message_thread_id"); thread > 0 {
		payload["message_thread_id"] = thread
	}
	// Silent delivery: a digest at 3am should not ring every phone in the group.
	if cfgBool(cfg, "silent") {
		payload["disable_notification"] = true
	}
	raw, err := doJSON(ctx, "POST", endpoint, nil, payload)
	if err != nil {
		return 0, err
	}
	var res struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("failed to parse Telegram response: %w (%s)", err, snippet(raw))
	}
	if res.OK {
		return kept, nil
	}
	// 429 is rate limiting and retries after backoff work; the rest (400 bad
	// parameters, 401 bad token, 403 blocked, 404 chat not found) are
	// configuration problems that retries will not fix.
	if res.ErrorCode == 429 {
		return 0, fmt.Errorf("Telegram rate limit: %s", res.Description)
	}
	return 0, Permanent(fmt.Errorf("Telegram returned error %d: %s", res.ErrorCode, res.Description))
}

// telegramParseMode reports the parse mode for this configuration. A custom
// body_template is plain text unless the template says otherwise: echoing the
// built-in "HTML" would make Telegram reject a body that is not HTML
// (and silently swallow angle brackets the template meant literally).
func telegramParseMode(cfg map[string]any) string {
	if cfgString(cfg, "body_template") != "" {
		return cfgString(cfg, "parse_mode")
	}
	return "HTML"
}

// telegramEndpoint builds the sendMessage URL. An empty base_url uses the
// official API; a non-empty one is for a self-hosted Bot API reverse proxy (a
// common need on networks where the official API is unreachable).
func telegramEndpoint(cfg map[string]any) (string, error) {
	base := cfgString(cfg, "base_url")
	if base == "" {
		base = "https://api.telegram.org"
	}
	base = strings.TrimSuffix(base, "/")
	token := cfgString(cfg, "bot_token")
	raw := base + "/bot" + token + "/sendMessage"
	u, err := url.Parse(raw)
	if err != nil {
		// Do not pass err through: the URL contains the Bot Token, and at this
		// point not even the address should be echoed.
		return "", fmt.Errorf("failed to build API URL (base URL: %s)", redactRequestTarget(base))
	}
	return u.String(), nil
}

// telegramHTML renders the HTML body and returns it together with the number of
// items actually written (see Channel.Send).
func telegramHTML(m Message) (string, int) {
	lim := m.limits()
	var b strings.Builder
	if m.Batch {
		// Telegram's limit is in **characters**, so packing is measured in
		// characters too (runeSize).
		footer := ""
		if m.HomeURL != "" {
			footer = fmt.Sprintf("\n\n<a href=\"%s\">View all on the platform</a>", telegramEscapeAttr(m.HomeURL))
		}
		kept := packItemCount(m.Items, telegramTextLimit, telegramReservedRunes, footer, runeSize, func(it Item, idx int) string {
			return telegramBatchLine(it, idx+1, lim)
		})
		items := m.Items[:kept]
		b.WriteString("<b>" + telegramEscape(telegramBatchTitle(m, items, len(m.Items))) + "</b>")
		// Severity counts of the items in THIS message, so the urgent line is
		// visible before any scrolling — the markdown and Feishu digests already
		// open with it.
		if bd := severityBreakdown(items); bd != "" {
			b.WriteString("\n" + telegramEscape(bd))
		}
		for i, it := range items {
			b.WriteString("\n" + telegramBatchLine(it, i+1, lim))
		}
		b.WriteString(footer)
		return TruncateHTML(b.String(), telegramTextLimit), kept
	}
	title := markdownTitle(m)
	if len(m.Items) > 0 && m.Items[0].Kind == EventTaskStatusChanged {
		title = telegramTaskTitle(m.Items[0])
	}
	b.WriteString("<b>" + telegramEscape(title) + "</b>\n")
	if len(m.Items) == 0 {
		return b.String(), 0
	}
	it := m.Items[0]
	if it.IsStatusChange() {
		b.WriteString(fmt.Sprintf("\n<b>Status change</b>: %s → %s",
			telegramEscape(StatusLabel(it.FromStatus)), telegramEscape(StatusLabel(it.ToStatus))))
	}
	if it.VulnClass != "" && it.VulnClass != it.Title() {
		b.WriteString("\n<b>Type</b>: " + telegramEscape(it.VulnClass))
	}
	if a := assetLine(it.Assets, lim.AssetsShown); a != "" {
		b.WriteString("\n<b>Assets</b>: " + telegramEscape(a))
	}
	if s := OneLine(it.Summary, lim.SummaryRunes); s != "" {
		b.WriteString("\n<b>Summary</b>: " + telegramEscape(s))
	}
	if it.DetailURL != "" {
		b.WriteString(fmt.Sprintf("\n\n<a href=\"%s\">View details</a>", telegramEscapeAttr(it.DetailURL)))
	}
	return TruncateHTML(b.String(), telegramTextLimit), 1
}

// telegramReservedRunes is reserved for the message title and a possible
// truncation notice (in characters).
const telegramReservedRunes = 160

// telegramBatchLine renders one entry of a digest. Unlike its siblings this
// returns HTML: the item title is a link when the backlink is known, so a
// reader taps straight into the finding instead of scrolling to a footer. The
// caller must not escape the result again; every untrusted field is escaped
// here.
func telegramBatchLine(it Item, idx int, lim Limits) string {
	if it.Kind == EventTaskStatusChanged {
		return fmt.Sprintf("%d. %s: %s → %s", idx, telegramEscape(telegramTaskTitle(it)),
			telegramEscape(StatusLabel(it.FromStatus)), telegramEscape(StatusLabel(it.ToStatus)))
	}
	title := "<b>" + telegramEscape(it.Title()) + "</b>"
	if it.DetailURL != "" {
		title = fmt.Sprintf("<a href=\"%s\">%s</a>", telegramEscapeAttr(it.DetailURL), telegramEscape(it.Title()))
	}
	line := fmt.Sprintf("%d. %s · %s", idx, telegramEscape(SeverityLabel(it.Severity)), title)
	if a := assetLine(it.Assets, lim.AssetsShown); a != "" {
		line += " — " + telegramEscape(a)
	}
	// A one-line summary keeps the digest triageable without a second tap; 60
	// characters matches the markdown family's digest compression.
	if s := OneLine(it.Summary, 60); s != "" {
		line += "\n    " + telegramEscape(s)
	}
	return line
}

func telegramTaskTitle(it Item) string {
	title := "Task status changed"
	if it.TaskID > 0 {
		title = fmt.Sprintf("Task #%d status changed", it.TaskID)
	}
	if name := OneLine(it.Name, 0); name != "" {
		title += " · " + name
	}
	return title
}

// telegramBatchTitle renders the title line of a digest message. The count is
// the number of items **actually included in this message**, not the batch
// total — otherwise readers would take the number in the header for the whole
// batch.
func telegramBatchTitle(m Message, items []Item, total int) string {
	label := "Vulnerability digest"
	for _, it := range m.Items {
		if it.Kind == EventTaskStatusChanged {
			label = "Notification digest"
			break
		}
	}
	title := fmt.Sprintf("%s · %d total", label, total)
	if extra := total - len(items); extra > 0 {
		title += fmt.Sprintf(" (showing the first %d, %d more in the next message)", len(items), extra)
	}
	if m.WindowMinutes > 0 {
		title = fmt.Sprintf("Last %d min · %s", m.WindowMinutes, title)
	}
	return title
}

// telegramEscape escapes HTML text content.
// Telegram only recognizes these three entities, so existing entities like &amp;
// get double-escaped — which is exactly the desired behavior: we want to display
// the raw characters, not let users inject HTML.
func telegramEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// telegramEscapeAttr escapes an HTML attribute value. On top of text escaping it
// also handles quotes — a quote in a URL would close the href attribute early
// and turn the rest into an injection point.
func telegramEscapeAttr(s string) string {
	s = telegramEscape(s)
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

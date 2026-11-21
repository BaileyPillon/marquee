package alerts

import (
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// field is one setting of a kind of channel.
type field struct {
	name   string
	secret bool
	def    string // used when the field is left empty
}

// specs lists the settings of each kind. Every value is text; a number (the SMTP
// port) may arrive as a JSON number and is kept as text.
var specs = map[string][]field{
	KindDiscord:  {{name: "url", secret: true}},
	KindNtfy:     {{name: "server", def: "https://ntfy.sh"}, {name: "topic"}, {name: "token", secret: true}},
	KindGotify:   {{name: "server"}, {name: "token", secret: true}},
	KindTelegram: {{name: "botToken", secret: true}, {name: "chatId"}},
	KindPushover: {{name: "appToken", secret: true}, {name: "userKey", secret: true}},
	KindEmail: {
		{name: "host"}, {name: "port", def: "587"}, {name: "security", def: "starttls"},
		{name: "username"}, {name: "password", secret: true}, {name: "from"}, {name: "to"},
	},
	KindWebhook: {{name: "url", secret: true}, {name: "secret", secret: true}},
}

func specOf(kind, name string) (field, bool) {
	for _, f := range specs[kind] {
		if f.name == name {
			return f, true
		}
	}
	return field{}, false
}

// text reads a config value as text: a string, or a number written as text.
func text(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", true
	case string:
		return strings.TrimSpace(t), true
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), true
		}
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	}
	return "", false
}

// merge works out the settings a channel will have: the stored ones (sealed
// text, as the database has them) under the incoming ones. A secret that arrives
// masked keeps the stored value; an empty field takes its default. The result
// holds the secrets in the clear.
func merge(box *secrets.Box, kind string, stored map[string]string, incoming map[string]any) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range specs[kind] {
		if f.secret && stored[f.name] != "" {
			plain, err := box.Open(stored[f.name])
			if err != nil {
				return nil, invalid("A saved secret cannot be read (was the master key replaced?). Enter it again.")
			}
			out[f.name] = plain
		} else if !f.secret {
			out[f.name] = stored[f.name]
		}
	}
	for name, raw := range incoming {
		f, ok := specOf(kind, name)
		if !ok {
			return nil, invalid("A " + kind + " channel has no setting called " + strconv.Quote(name) + ".")
		}
		v, ok := text(raw)
		if !ok {
			return nil, invalid("The setting " + strconv.Quote(name) + " must be text.")
		}
		if f.secret && strings.HasPrefix(v, settings.MaskPrefix) {
			continue // unchanged placeholder: keep what is stored
		}
		out[name] = v
	}
	for _, f := range specs[kind] {
		if out[f.name] == "" {
			out[f.name] = f.def
		}
	}
	return out, nil
}

var (
	topicRe  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	botRe    = regexp.MustCompile(`^\d{3,}:[A-Za-z0-9_-]{10,}$`)
	chatIDRe = regexp.MustCompile(`^(-?\d{1,20}|@[A-Za-z0-9_]{3,})$`)
)

func httpURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return invalid("The " + name + " must be a web address starting with http:// or https://.")
	}
	return nil
}

func required(kind string, cfg map[string]string, names ...string) error {
	for _, n := range names {
		if cfg[n] == "" {
			return invalid("A " + kind + " channel needs its " + n + ".")
		}
	}
	return nil
}

// validate checks a channel's settings (secrets in the clear).
func validate(kind string, cfg map[string]string) error {
	switch kind {
	case KindDiscord:
		if err := required(kind, cfg, "url"); err != nil {
			return err
		}
		return httpURL("webhook URL", cfg["url"])
	case KindNtfy:
		if err := required(kind, cfg, "topic"); err != nil {
			return err
		}
		if !topicRe.MatchString(cfg["topic"]) {
			return invalid("An ntfy topic is letters, digits, dashes and underscores, up to 64 of them.")
		}
		return httpURL("ntfy server", cfg["server"])
	case KindGotify:
		if err := required(kind, cfg, "server", "token"); err != nil {
			return err
		}
		return httpURL("Gotify server", cfg["server"])
	case KindTelegram:
		if err := required(kind, cfg, "botToken", "chatId"); err != nil {
			return err
		}
		if !botRe.MatchString(cfg["botToken"]) {
			return invalid("That does not look like a Telegram bot token (it is digits, a colon and letters).")
		}
		if !chatIDRe.MatchString(cfg["chatId"]) {
			return invalid("A Telegram chat id is a number (or @channelname).")
		}
	case KindPushover:
		return required(kind, cfg, "appToken", "userKey")
	case KindEmail:
		if err := required(kind, cfg, "host", "from", "to"); err != nil {
			return err
		}
		if p, err := strconv.Atoi(cfg["port"]); err != nil || p < 1 || p > 65535 {
			return invalid("The SMTP port is a number from 1 to 65535.")
		}
		switch cfg["security"] {
		case "starttls", "tls", "none":
		default:
			return invalid(`The email security is "starttls", "tls" or "none".`)
		}
		if _, err := mail.ParseAddress(cfg["from"]); err != nil {
			return invalid("The From address is not a valid email address.")
		}
		if len(recipients(cfg["to"])) == 0 {
			return invalid("The To address is not a valid email address.")
		}
		if cfg["username"] != "" && cfg["password"] == "" {
			return invalid("An SMTP user needs a password.")
		}
	case KindWebhook:
		if err := required(kind, cfg, "url"); err != nil {
			return err
		}
		return httpURL("webhook URL", cfg["url"])
	}
	return nil
}

// recipients reads "a@x.test, b@y.test" into valid addresses.
func recipients(to string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(to, func(r rune) bool { return r == ',' || r == ';' }) {
		a, err := mail.ParseAddress(strings.TrimSpace(part))
		if err != nil {
			return nil
		}
		out = append(out, a.Address)
	}
	return out
}

// seal turns settings (secrets in the clear) into what the database keeps:
// every field as text, the secrets sealed.
func seal(box *secrets.Box, kind string, cfg map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range specs[kind] {
		v := cfg[f.name]
		if f.secret && v != "" {
			sealed, err := box.Seal(v)
			if err != nil {
				return nil, err
			}
			v = sealed
		}
		out[f.name] = v
	}
	return out, nil
}

// mask turns stored settings into what the API shows: secrets masked, the SMTP
// port as a number.
func mask(box *secrets.Box, kind string, stored map[string]string) map[string]any {
	out := map[string]any{}
	for _, f := range specs[kind] {
		v := stored[f.name]
		switch {
		case f.secret && v != "":
			if plain, err := box.Open(v); err == nil {
				out[f.name] = settings.Mask(plain)
			} else {
				out[f.name] = settings.MaskPrefix // unreadable, but never show the sealed text
			}
		case f.secret:
			out[f.name] = ""
		case f.name == "port":
			if n, err := strconv.Atoi(v); err == nil {
				out[f.name] = n
			} else {
				out[f.name] = v
			}
		default:
			out[f.name] = v
		}
	}
	return out
}

// card gera dark_mode.svg e light_mode.svg no estilo neofetch para o README do perfil.
// Com GITHUB_TOKEN definido, atualiza data/stats.json pela API do GitHub antes de desenhar.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

type Config struct {
	User     string    `json:"user"`
	Header   string    `json:"header"`
	Birthday string    `json:"birthday"`
	Width    int       `json:"width"`
	Sections []Section `json:"sections"`
}

type Section struct {
	Title  string      `json:"title"`
	Fields [][2]string `json:"fields"`
}

type Stats struct {
	Repos       int    `json:"repos"`
	Contributed int    `json:"contributed"`
	Stars       int    `json:"stars"`
	Followers   int    `json:"followers"`
	Commits     int    `json:"commits"`
	Additions   int    `json:"additions"`
	Deletions   int    `json:"deletions"`
	Updated     string `json:"updated"`
}

type Theme struct {
	Bg, Border, Text, Key, Value, Dots, Add, Del string
}

var themes = map[string]Theme{
	"dark_mode.svg":  {"#161b22", "#30363d", "#c9d1d9", "#ffa657", "#a5d6ff", "#616e7f", "#3fb950", "#f85149"},
	"light_mode.svg": {"#f6f8fa", "#d0d7de", "#24292f", "#953800", "#0a3069", "#c2cfde", "#1a7f37", "#cf222e"},
}

func main() {
	var cfg Config
	must(readJSON("card.json", &cfg))

	var st Stats
	_ = readJSON("data/stats.json", &st)
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		fresh, err := fetchStats(tok, cfg.User)
		if err != nil {
			log.Printf("aviso: stats não atualizadas, usando cache: %v", err)
		} else {
			st = fresh
			st.Updated = time.Now().UTC().Format(time.RFC3339)
			must(writeJSON("data/stats.json", st))
		}
	}

	artBytes, err := os.ReadFile("assets/art.txt")
	must(err)
	art := strings.Split(strings.TrimRight(strings.ReplaceAll(string(artBytes), "\r", ""), "\n"), "\n")

	lines := buildLines(cfg, st)
	for file, th := range themes {
		must(os.WriteFile(file, []byte(render(art, lines, th)), 0o644))
		log.Printf("gerado %s", file)
	}
}

// seg é um trecho de texto com a classe CSS que define sua cor.
type seg struct{ text, class string }

func buildLines(cfg Config, st Stats) [][]seg {
	w := cfg.Width
	var out [][]seg

	out = append(out, rule(cfg.Header, w, ""))
	for i, s := range cfg.Sections {
		if i > 0 {
			out = append(out, nil)
		}
		if s.Title != "" {
			out = append(out, rule(s.Title, w, "— "))
		}
		for _, f := range s.Fields {
			val := f[1]
			if val == "{uptime}" {
				if cfg.Birthday == "" {
					continue
				}
				val = uptime(cfg.Birthday)
			}
			out = append(out, field(f[0], val, w))
		}
	}

	out = append(out, nil, rule("GitHub Stats", w, "— "))
	half := (w - 3) / 2
	out = append(out,
		pair(field("Repos", fmt.Sprintf("%s {Contrib: %s}", num(st.Repos), num(st.Contributed)), half),
			field("Stars", num(st.Stars), w-3-half)),
		pair(field("Commits", num(st.Commits), half),
			field("Followers", num(st.Followers), w-3-half)),
		[]seg{
			{"Linhas de código", "key"}, {": ", ""},
			{num(st.Additions - st.Deletions), "value"}, {" ( ", ""},
			{num(st.Additions) + "++", "add"}, {", ", ""},
			{num(st.Deletions) + "--", "del"}, {" )", ""},
		})
	return out
}

// rule monta um título seguido de traços até a largura da coluna.
func rule(title string, w int, prefix string) []seg {
	n := w - utf8.RuneCountInString(prefix+title) - 1
	return []seg{{prefix, ""}, {title, "value"}, {" " + strings.Repeat("—", max(n, 0)), "dots"}}
}

// field monta "Chave: ....... valor" ocupando exatamente w caracteres.
func field(key, val string, w int) []seg {
	n := w - utf8.RuneCountInString(key) - utf8.RuneCountInString(val) - 3
	return []seg{{key, "key"}, {": ", ""}, {strings.Repeat(".", max(n, 1)), "dots"}, {" " + val, "value"}}
}

func pair(a, b []seg) []seg {
	return append(append(a, seg{" | ", ""}), b...)
}

func uptime(birthday string) string {
	b, err := time.Parse("2006-01-02", birthday)
	if err != nil {
		return birthday
	}
	now := time.Now()
	y, m, d := now.Year()-b.Year(), int(now.Month()-b.Month()), now.Day()-b.Day()
	if d < 0 {
		m--
		d += time.Date(now.Year(), now.Month(), 0, 0, 0, 0, 0, time.UTC).Day()
	}
	if m < 0 {
		y--
		m += 12
	}
	return fmt.Sprintf("%d anos, %d meses, %d dias", y, m, d)
}

func num(n int) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

const (
	fontSize   = 16
	lineHeight = 20
	charWidth  = 9.6 // largura aproximada de um caractere monoespaçado a 16px
	padX, padY = 20, 34
	gap        = 3 // colunas entre a arte e o texto
)

func render(art []string, lines [][]seg, th Theme) string {
	artCols := 0
	for _, l := range art {
		artCols = max(artCols, utf8.RuneCountInString(l))
	}
	textX := padX + float64(artCols+gap)*charWidth
	textCols := 0
	for _, l := range lines {
		n := 0
		for _, s := range l {
			n += utf8.RuneCountInString(s.text)
		}
		textCols = max(textCols, n)
	}
	width := int(textX + float64(textCols)*charWidth + padX)
	rows := max(len(art), len(lines))
	height := padY*2 + (rows-1)*lineHeight

	artY := padY + (rows-len(art))*lineHeight/2
	textY := padY + (rows-len(lines))*lineHeight/2

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" font-family="ConsolasFallback,Consolas,'Courier New',monospace" width="%dpx" height="%dpx" font-size="%dpx">
<style>
@font-face { src: local('Consolas'), local('Consolas Bold'); font-family: 'ConsolasFallback'; font-display: swap; -webkit-size-adjust: 109%%; size-adjust: 109%%; }
text, tspan { white-space: pre; }
.key { fill: %s; } .value { fill: %s; } .dots { fill: %s; } .add { fill: %s; } .del { fill: %s; }
</style>
<rect width="%dpx" height="%dpx" fill="%s" stroke="%s" rx="15"/>
`, width, height, fontSize, th.Key, th.Value, th.Dots, th.Add, th.Del, width-1, height-1, th.Bg, th.Border)

	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="%s">`+"\n", padX, artY, th.Text)
	for i, l := range art {
		fmt.Fprintf(&b, `<tspan x="%d" y="%d">%s</tspan>`+"\n", padX, artY+i*lineHeight, esc(l))
	}
	b.WriteString("</text>\n")

	fmt.Fprintf(&b, `<text x="%.0f" y="%d" fill="%s">`+"\n", textX, textY, th.Text)
	for i, l := range lines {
		fmt.Fprintf(&b, `<tspan x="%.0f" y="%d">`, textX, textY+i*lineHeight)
		for _, s := range l {
			if s.class == "" {
				b.WriteString(esc(s.text))
			} else {
				fmt.Fprintf(&b, `<tspan class="%s">%s</tspan>`, s.class, esc(s.text))
			}
		}
		b.WriteString("</tspan>\n")
	}
	b.WriteString("</text>\n</svg>\n")
	return b.String()
}

func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

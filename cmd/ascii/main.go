// ascii converte uma foto em arte ASCII para o card do perfil.
// Uso: go run ./cmd/ascii -in foto.png -cols 44 > art.txt
package main

import (
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"sort"
	"strings"
)

func main() {
	in := flag.String("in", "", "imagem de entrada (png/jpg)")
	cols := flag.Int("cols", 44, "largura em caracteres")
	aspect := flag.Float64("aspect", 0.5, "proporção largura/altura do caractere")
	ramp := flag.String("ramp", " .:-=+*#%@", "caracteres do mais claro ao mais denso")
	invert := flag.Bool("invert", false, "pixel escuro vira caractere denso")
	mask := flag.Float64("mask", 0.95, "raio da máscara elíptica (0 desliga)")
	cx := flag.Float64("cx", 0.5, "centro X da máscara (0-1)")
	cy := flag.Float64("cy", 0.45, "centro Y da máscara (0-1)")
	crop := flag.String("crop", "", "recorte x0,y0,x1,y1 em pixels")
	flag.Parse()

	f, err := os.Open(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	b := img.Bounds()
	if *crop != "" {
		var x0, y0, x1, y1 int
		fmt.Sscanf(*crop, "%d,%d,%d,%d", &x0, &y0, &x1, &y1)
		b = image.Rect(x0, y0, x1, y1).Intersect(b)
	}
	cellW := float64(b.Dx()) / float64(*cols)
	cellH := cellW / *aspect
	rows := int(float64(b.Dy()) / cellH)

	// Luminância média de cada célula.
	lum := make([]float64, rows**cols)
	for r := 0; r < rows; r++ {
		for c := 0; c < *cols; c++ {
			x0, y0 := b.Min.X+int(float64(c)*cellW), b.Min.Y+int(float64(r)*cellH)
			x1, y1 := b.Min.X+int(float64(c+1)*cellW), b.Min.Y+int(float64(r+1)*cellH)
			var sum float64
			var n int
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					cr, cg, cb, _ := img.At(x, y).RGBA()
					sum += (0.299*float64(cr) + 0.587*float64(cg) + 0.114*float64(cb)) / 65535
					n++
				}
			}
			if n > 0 {
				lum[r**cols+c] = sum / float64(n)
			}
		}
	}

	// Estica o contraste entre os percentis 2 e 98.
	sorted := append([]float64(nil), lum...)
	sort.Float64s(sorted)
	lo, hi := sorted[len(sorted)*2/100], sorted[len(sorted)*98/100]

	chars := []rune(*ramp)
	var out strings.Builder
	for r := 0; r < rows; r++ {
		line := make([]rune, *cols)
		for c := 0; c < *cols; c++ {
			if *mask > 0 {
				dx := (float64(c)+0.5)/float64(*cols) - *cx
				dy := (float64(r)+0.5)/float64(rows) - *cy
				if math.Sqrt(dx*dx*4+dy*dy*4) > *mask {
					line[c] = ' '
					continue
				}
			}
			v := (lum[r**cols+c] - lo) / (hi - lo)
			v = math.Max(0, math.Min(1, v))
			if *invert {
				v = 1 - v
			}
			line[c] = chars[int(math.Round(v*float64(len(chars)-1)))]
		}
		out.WriteString(strings.TrimRight(string(line), " "))
		out.WriteByte('\n')
	}
	fmt.Print(strings.TrimRight(out.String(), "\n ") + "\n")
}

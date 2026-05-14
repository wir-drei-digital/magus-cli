package output

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// PrintTable writes a simple aligned table. Each row has the same number of
// columns. Headers are uppercased.
func PrintTable(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	writeRow(os.Stdout, headers, widths, true)
	for _, row := range rows {
		writeRow(os.Stdout, row, widths, false)
	}
}

func writeRow(w io.Writer, row []string, widths []int, upper bool) {
	parts := make([]string, len(row))
	for i, cell := range row {
		if upper {
			cell = strings.ToUpper(cell)
		}
		parts[i] = padRight(cell, widths[i])
	}
	_, _ = fmt.Fprintln(w, strings.Join(parts, "  "))
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

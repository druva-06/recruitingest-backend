package pdfparser

import (
	"fmt"

	"github.com/ledongthuc/pdf"
)

// ExtractText reads a PDF file and returns its concatenated text content.
func ExtractText(filePath string) (string, error) {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}
	defer f.Close()

	var text string
	totalPage := r.NumPage()

	for pageIndex := 1; pageIndex <= totalPage; pageIndex++ {
		p := r.Page(pageIndex)
		if p.V.IsNull() {
			continue
		}
		pageText, err := p.GetPlainText(nil)
		if err != nil {
			return "", fmt.Errorf("failed to get text from page %d: %w", pageIndex, err)
		}
		text += pageText
	}

	return text, nil
}

package mail

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	texttemplate "text/template"
)

//go:embed templates/*.html templates/*.txt
var mailTemplates embed.FS

var (
	htmlMailTemplates = htmltemplate.Must(htmltemplate.New("mail").ParseFS(mailTemplates, "templates/*.html"))
	textMailTemplates = texttemplate.Must(texttemplate.New("mail").ParseFS(mailTemplates, "templates/*.txt"))
)

type mailTemplateData struct {
	VerificationURL string
	Code            string
	NewEmail        string
}

func renderMailTemplates(name string, data mailTemplateData) (string, string, error) {
	var plain, markup bytes.Buffer
	if err := textMailTemplates.ExecuteTemplate(&plain, name+".txt", data); err != nil {
		return "", "", fmt.Errorf("render plain mail template: %w", err)
	}
	if err := htmlMailTemplates.ExecuteTemplate(&markup, name+".html", data); err != nil {
		return "", "", fmt.Errorf("render HTML mail template: %w", err)
	}
	return plain.String(), markup.String(), nil
}

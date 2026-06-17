package showcase

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Generator struct {
	TemplatesDir string
	ContentDir   string
	OutputDir    string
}

func NewGenerator(templatesDir, contentDir, outputDir string) *Generator {
	return &Generator{
		TemplatesDir: templatesDir,
		ContentDir:   contentDir,
		OutputDir:    outputDir,
	}
}

func (g *Generator) Generate() error {
	if err := os.MkdirAll(g.OutputDir, 0755); err != nil {
		return err
	}

	landingConfig, err := g.loadLandingConfig()
	if err != nil {
		return err
	}

	currentYear := time.Now().Year()

	data := TemplateData{
		Landing: landingConfig,
		Year:    currentYear,
	}

	// Copy styles.css to output directory
	cssInput, err := os.ReadFile(filepath.Join(g.TemplatesDir, "styles.css"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(g.OutputDir, "styles.css"), cssInput, 0644); err != nil {
		return err
	}

	// Render Landing (index.html)
	if err := g.renderWithBase("index.html", "index.html", data); err != nil {
		return err
	}

	// Render llms.txt
	if err := g.renderRawTemplate("llms.txt", "llms.txt", data); err != nil {
		return err
	}

	return nil
}

func (g *Generator) loadLandingConfig() (*SiteConfig, error) {
	data, err := os.ReadFile(filepath.Join(g.ContentDir, "landing.yaml"))
	if err != nil {
		return nil, err
	}
	var config SiteConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

func (g *Generator) getFuncs() template.FuncMap {
	return template.FuncMap{
		"dict": func(values ...interface{}) (map[string]interface{}, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("invalid dict call")
			}
			dict := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
		"list": func(values ...interface{}) []interface{} {
			return values
		},
	}
}

func (g *Generator) renderWithBase(tmplName, outName string, data interface{}) error {
	basePath := filepath.Join(g.TemplatesDir, "base.html")
	tmplPath := filepath.Join(g.TemplatesDir, tmplName)

	tmpl, err := template.New("base.html").Funcs(g.getFuncs()).ParseFiles(basePath, tmplPath)
	if err != nil {
		return err
	}

	outFile, err := os.Create(filepath.Join(g.OutputDir, outName))
	if err != nil {
		return err
	}
	defer outFile.Close()

	return tmpl.ExecuteTemplate(outFile, "base.html", data)
}

func (g *Generator) renderRawTemplate(tmplName, outName string, data interface{}) error {
	tmplPath := filepath.Join(g.TemplatesDir, tmplName)
	tmpl, err := template.New(tmplName).Funcs(g.getFuncs()).ParseFiles(tmplPath)
	if err != nil {
		return err
	}

	outFile, err := os.Create(filepath.Join(g.OutputDir, outName))
	if err != nil {
		return err
	}
	defer outFile.Close()

	return tmpl.Execute(outFile, data)
}

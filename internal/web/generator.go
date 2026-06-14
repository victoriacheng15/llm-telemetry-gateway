package web

import (
	"html/template"
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
	// Ensure output directory exists

	// Load landing configuration

	// Render landing (index.html)

	// Render llms.txt

	return nil
}

func (g *Generator) loadLandingConfig() (*SiteConfig, error) {
	// Read and unmarshal landing.yaml config file
	return nil, nil
}

func (g *Generator) getFuncs() template.FuncMap {
	// Return custom template function map (dict and list helper functions)
	return nil
}

func (g *Generator) renderWithBase(tmplName, outName string, data interface{}) error {
	// Compile and execute the template wrapped in base.html
	return nil
}

func (g *Generator) renderRawTemplate(tmplName, outName string, data interface{}) error {
	// Compile and execute the raw template directly
	return nil
}

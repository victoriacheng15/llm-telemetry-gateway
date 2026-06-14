package web

type SiteConfig struct {
	Header       HeaderConfig        `yaml:"header"`
	SystemSpec   SystemSpecification `yaml:"system_specification"`
	Hero         HeroConfig          `yaml:"hero"`
	WhatIs       WhatIsConfig        `yaml:"what_is"`
	KeyFeatures  KeyFeaturesConfig   `yaml:"key_features"`
	WhyItMatters WhyItMattersConfig  `yaml:"why_it_matters"`
	Footer       FooterConfig        `yaml:"footer"`
}

type HeaderConfig struct {
	ProjectName string `yaml:"project_name"`
	SiteURL     string `yaml:"site_url"`
}

type SystemSpecification struct {
	Objective           string `yaml:"objective"`
	Stack               string `yaml:"stack"`
	Pattern             string `yaml:"pattern"`
	EntryPoint          string `yaml:"entry_point"`
	PersistenceStrategy string `yaml:"persistence_strategy"`
	Observability       string `yaml:"observability"`
	MachineRegistry     string `yaml:"machine_registry"`
}

type HeroConfig struct {
	Headline         string `yaml:"headline"`
	SubHeadline      string `yaml:"sub_headline"`
	BriefDescription string `yaml:"brief_description"`
	CTAText          string `yaml:"cta_text"`
	CTALink          string `yaml:"cta_link"`
}

type WhatIsConfig struct {
	Title   string   `yaml:"title"`
	Content []string `yaml:"content"`
}

type KeyFeaturesConfig struct {
	Title    string    `yaml:"title"`
	Features []Feature `yaml:"features"`
}

type Feature struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Icon        string `yaml:"icon"`
}

type WhyItMattersConfig struct {
	Title  string   `yaml:"title"`
	Points []string `yaml:"points"`
}

type FooterConfig struct {
	Author       string `yaml:"author"`
	GithubLink   string `yaml:"github_link"`
	LinkedinLink string `yaml:"linkedin_link"`
}

type TemplateData struct {
	Landing *SiteConfig
	Year    int
}

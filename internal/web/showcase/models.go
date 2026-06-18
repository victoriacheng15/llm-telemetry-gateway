package showcase

type SiteConfig struct {
	Header HeaderConfig        `yaml:"header"`
	LLMS   SystemSpecification `yaml:"llms"`
	Tech   TechConfig          `yaml:"tech"`
	Proof  ProofConfig         `yaml:"proof"`
	Reach  ReachConfig         `yaml:"reach"`
	Footer FooterConfig        `yaml:"footer"`
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
}

type TechConfig struct {
	ProxyDataPlane             PillarConfig `yaml:"proxy_data_plane"`
	AIOpsSidecar               PillarConfig `yaml:"aiops_sidecar"`
	PodBoundaryInstrumentation PillarConfig `yaml:"pod_boundary_instrumentation"`
}

type PillarConfig struct {
	Description string `yaml:"description"`
}

type ProofConfig struct {
	ChaosResilience           PillarConfig `yaml:"chaos_resilience"`
	TelemetryPipeline         PillarConfig `yaml:"telemetry_pipeline"`
	DeclarativeInfrastructure PillarConfig `yaml:"declarative_infrastructure"`
}

type ReachConfig struct {
	ArchitectureBlueprint ArchitectureBlueprintConfig `yaml:"architecture_blueprint"`
	HumblePivots          []HumblePivotConfig         `yaml:"humble_pivots"`
	ObjectiveClarity      PillarConfig                `yaml:"objective_clarity"`
	VerifiableOutputs     []VerifiableOutputConfig    `yaml:"verifiable_outputs"`
}

type ArchitectureBlueprintConfig struct {
	DiagramASCII string `yaml:"diagram_ascii"`
}

type HumblePivotConfig struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

type VerifiableOutputConfig struct {
	Title          string `yaml:"title"`
	TerminalOutput string `yaml:"terminal_output"`
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

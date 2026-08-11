package orchestrator

// ScaffoldPlan represents the scaffolding plan for a rig
type ScaffoldPlan struct {
	Kind       string            // "host-run", "single-container", "multi-service"
	Stack      string            // "python", "node", "go", "hybrid", "generic"
	LayoutRoot string
	Port       int
	HarnessDir string
	BaseURL    string
	Services   []ScaffoldService
}

// ScaffoldService represents a service in the scaffold plan
type ScaffoldService struct {
	Name     string
	BuildDir string // relative to layout root, "." for root
	Image    string // if no build
	Port     int
	Stack    string // "python", "node", "go", "generic"
	Public   bool   // exposed to browser
	Health   string // healthcheck command
	Env      map[string]string
}

package isles

type GameConnection struct {
	Edition           string `json:"edition"`
	Provider          string `json:"provider"`
	Version           string `json:"version"`
	LinkedJavaAccount bool   `json:"linkedJavaAccount"`
}
type CrossplaySnapshot struct {
	JavaVersion     string   `json:"javaVersion"`
	BedrockVersions []string `json:"bedrockVersions"`
}

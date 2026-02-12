package config

const (
	PresetHigh     = "high"
	PresetStandard = "standard"
	PresetLow      = "low"
)

type Config struct {
	InputPath        string
	OutputPath       string
	Preset           string
	Quality          int
	Workers          int
	SegmentSeconds   int
	DisableSegResume bool
}

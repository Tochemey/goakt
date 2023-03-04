package actors

// StrategyDirective represents the supervisor strategy directive
type StrategyDirective int

const (
	RestartDirective StrategyDirective = iota
	StopDirective
)

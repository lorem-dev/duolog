package workerenv

// Signal names one piece of billing route evidence and the settings layer or
// source it came from. It carries names only, never the value behind them.
type Signal struct {
	Name  string `json:"name"`
	Layer string `json:"layer"`
}

package actors

type Watcher struct {
	Parent  *pid       // the Parent of the actor watching
	ErrChan chan error // the channel where to pass error message
	Done    chan Unit
}

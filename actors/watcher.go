package actors

type watcher struct {
	pid     *PID       // the pid of the actor watching
	errChan chan error // the channel where to pass error message
}

package kubernetes

const (
	Namespace        string = "namespace"
	ActorSystemName         = "actor_system_name"
	RemotingPortName        = "remoting"
	ApplicationName         = "app_name"
	RaftPortName            = "raft"
)

// Option represents the kubernetes provider option
type Option struct {
	// KubeConfig represents the kubernetes configuration
	KubeConfig string
	// NameSpace specifies the namespace
	NameSpace string
	// The actor system name
	ActorSystemName string
	// Specifies the remoting port name
	// This port is necessary to send remote messages to node
	RemotingPortName string
	// ApplicationName specifies the running application
	ApplicationName string
	// RaftPortName specifies the raft port name
	RaftPortName string
}

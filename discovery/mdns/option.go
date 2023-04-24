package mdns

const (
	Service = "service"
	Domain  = "domain"
)

// Option represents the mDNS provider option
type Option struct {
	// Service specifies the service instance
	Service string
	// Specifies the service domain
	Domain string
}

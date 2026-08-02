// Package crp defines the Cloud Resource Provider (CRP) adapter interface.
//
// A CRP is the per-provider driver (azure, aws, ...) that creates, starts,
// stops, deallocates/hibernates and destroys worker VMs on behalf of the
// Cloud Elastic Controller (CEC). It is interface-only so M1 can ship with a
// logging stub (LoggingCRP) and M2 can attach a real Azure implementation.
//
// Key contract: Ensure() returns a VM handle (vmID) IMMEDIATELY, before the
// VM has booted. IP/hostname are not known at that point, but the vmID is the
// stable identity assigned at creation time; it is what the CEC binds jobs to
// (see docs/cloud-elastic-event-driven-design.md §12.3).
package crp

import "time"

// VM represents a provisioned cloud worker.
type VM struct {
	ID         string // stable cloud handle: Azure resource id / AWS instance id
	Name       string // cloud VM name (may be random)
	SKU        string // VM size/sku, e.g. "Standard_D8s_v3"
	IP         string // private IP, empty until boot
	Hostname   string // node hostname, empty until boot
	State      string // requested state string (see VMState*)
	Location   string
	ResourceGroup string
	CreatedAt  time.Time
}

// VM lifecycle states reported by a CRP.
const (
	VMStateCreating     = "creating"
	VMStateRunning      = "running"
	VMStateStopped      = "deallocated" // stopped/deallocated
	VMStateHibernated   = "hibernated"
	VMStateTerminated   = "terminated"
)

// EnsureRequest describes what the CEC wants provisioned for a cloud pool.
type EnsureRequest struct {
	Provider    string // queue.cloud_provider: "azure" | "aws" | ...
	SKU         string // queue.cloud_vm_sku
	Count       int    // how many VMs to ensure exist (capacity)
	SubnetID    string // queue.cloud_subnet_id (where to place VMs)
	ImageID     string // queue.cloud_image_id
	DiskSize    int    // queue.cloud_disk_size (GB, 0 = default)
	DiskType    string // queue.cloud_disk_type
	SSHKey      string // queue.cloud_ssh_key (authorized for worker login)
	Location    string // queue.cloud_location
	ResourceGroup string // queue.cloud_rg_name
	MinNodes    int    // queue.cloud_min_nodes
	MaxNodes    int    // queue.cloud_max_nodes
}

// VMRef uniquely identifies a VM.
type VMRef struct {
	Provider string
	VMID     string
}

// Provider is the interface the CEC uses to talk to a cloud.
type Provider interface {
	// Name returns the provider name (e.g. "azure").
	Name() string

	// Ensure provisions vms until the pool has at least Count running VMs of
	// SKU. It must return the VM handles (with ID populated) immediately,
	// before boot, and it returns quickly; boot happens asynchronously. The
	// slice should have length >= Count of VMs that now exist (including any
	// already running / already being created).
	Ensure(req EnsureRequest) ([]VM, error)

	// Describe returns the current state of a single VM by ID.
	Describe(ref VMRef) (VM, error)

	// Reclaim stops/destroys a VM per the queue's reclaim policy
	// ("deallocate" stops+deallocates, "hibernate" stops+hibernates the first
	// time, destroy removes it entirely).
	Reclaim(ref VMRef, policy string, destroy bool) error

	// Resume starts a previously stopped/hibernated VM.
	Resume(ref VMRef) error

	// Health returns a provider health check result.
	Health(ref VMRef) error
}

// StubProvider is an in-memory Provider that logs instead of touching a cloud.
// It useful for M1 end-to-end testing of the CEC/scheduler path without real
// cloud calls. Each Ensure returns immediately with synthetic VM IDs.
type StubProvider struct {
	NameValue string
	nextID    int
	inFlight  map[string]*VM // vmID -> VM (treats them as "still booting")
}

// NewStubProvider creates a logging-only provider for testing.
func NewStubProvider(name string) *StubProvider {
	return &StubProvider{NameValue: name, nextID: 1, inFlight: make(map[string]*VM)}
}

// Name implements Provider.
func (p *StubProvider) Name() string { return p.NameValue }

// Ensure implements Provider: logs and returns synthetic VM handles.
func (p *StubProvider) Ensure(req EnsureRequest) ([]VM, error) {
	var out []VM
	for i := 0; i < req.Count; i++ {
		p.nextID++
		vm := VM{
			ID:            p.NameValue + "-vm-" + itoa(p.nextID),
			Name:          p.NameValue + "-node-" + itoa(p.nextID),
			SKU:           req.SKU,
			State:         VMStateCreating,
			Location:      req.Location,
			ResourceGroup: req.ResourceGroup,
			CreatedAt:     time.Now(),
		}
		p.inFlight[vm.ID] = &vm
		out = append(out, vm)
	}
	return out, nil
}

// Describe implements Provider.
func (p *StubProvider) Describe(ref VMRef) (VM, error) {
	if vm, ok := p.inFlight[ref.VMID]; ok {
		// Simulate eventual readiness on subsequent describes.
		if vm.State == VMStateCreating {
			vm.State = VMStateRunning
			vm.IP = "10.10.0." + itoa(p.nextID)
			vm.Hostname = vm.Name
		}
		return *vm, nil
	}
	return VM{}, nil
}

// Reclaim implements Provider.
func (p *StubProvider) Reclaim(ref VMRef, policy string, destroy bool) error {
	delete(p.inFlight, ref.VMID)
	return nil
}

// Resume implements Provider.
func (p *StubProvider) Resume(ref VMRef) error { return nil }

// Health implements Provider.
func (p *StubProvider) Health(ref VMRef) error { return nil }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

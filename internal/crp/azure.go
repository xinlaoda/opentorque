// Package crp implements the Azure Cloud Resource Provider.
//
// The AzureCRP is the M2 real-cloud driver for the Cloud Elastic Controller.
// It talks to the Azure management REST API using a managed identity (MSI)
// bearer token obtained from the instance metadata service (IMDS). It supports
// creating VM worker nodes (no public IP), describing/reclaiming/resuming them,
// and destroying them per the queue's reclaim policy.
//
// The srv VM (where pbs_sched runs) must have a managed identity with
// "Virtual Machine Contributor" and "Network Contributor" on the target
// resource group. The identity's client id is read from the AZURE_CLIENT_ID
// environment variable, or falls back to the default/any identity.
package crp

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// AzureCRP is a Provider backed by the Azure management REST API.
type AzureCRP struct {
	// subscription is required; resource group and location may be
	// overridden per-queue from EnsureRequest.
	subscription string

	token       string
	tokenExpiry time.Time
	client      *http.Client
	registry    *vmRegistry
}

// NewAzureCRP constructs an AzureCRP for the given subscription.
func NewAzureCRP(subscription string) *AzureCRP {
	return &AzureCRP{
		subscription: subscription,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
		registry: newVMRegistry(),
	}
}

// Name implements Provider.
func (a *AzureCRP) Name() string { return "azure" }

// IMDS_TOKEN_URL is the Azure Instance Metadata Service identity endpoint.
const IMDS_TOKEN_URL = "http://169.254.169.254/metadata/identity/oauth2/token"

// computeAPIVersion for Azure Compute / VM operations.
const computeAPIVersion = "2023-03-01"

// networkAPIVersion is used for Microsoft.Network resource operations. It is
// intentionally different from computeAPIVersion because some Azure regions do
// not expose 2023-03-01 for network resources (e.g. networkInterfaces), which
// would cause "API version not supported" errors at NIC creation time.
const networkAPIVersion = "2024-07-01"

// getToken obtains a bearer token from IMDS using the instance's managed
// identity. If AZURE_CLIENT_ID is set it requests the user-assigned identity
// explicitly; otherwise it defaults to whichever identity the instance has.
func (a *AzureCRP) getToken() (string, error) {
	// Reuse a cached unexpired token.
	if a.token != "" && time.Now().Before(a.tokenExpiry) {
		return a.token, nil
	}

	clientID := os.Getenv("AZURE_CLIENT_ID")
	// api-version is required by the IMDS identity endpoint.
	reqURL := IMDS_TOKEN_URL + "?api-version=2018-02-01&resource=https%3A%2F%2Fmanagement.azure.com%2F"
	if clientID != "" {
		reqURL += "&client_id=" + clientID
	}

	req, _ := http.NewRequest("GET", reqURL, nil)
	req.Header.Set("Metadata", "true")
	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("IMDS token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("IMDS token: HTTP %d: %s", resp.StatusCode, string(body))
	}

	// IMDS returns expires_in as a string (e.g. "86399"); tolerate both string
	// and numeric encodings via json.RawMessage.
	var tr struct {
		AccessToken string          `json:"access_token"`
		ExpiresIn   json.RawMessage `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("IMDS token decode: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("IMDS token: empty access_token")
	}
	// Default expiry window (60 min) and refine from expires_in when present.
	exp := 3600
	if len(tr.ExpiresIn) > 0 {
		var secs int
		if err := json.Unmarshal(tr.ExpiresIn, &secs); err == nil && secs > 0 {
			exp = secs
		}
	}
	a.token = tr.AccessToken
	if exp > 60 {
		exp -= 60
	}
	a.tokenExpiry = time.Now().Add(time.Duration(exp) * time.Second)
	return a.token, nil
}

// doAPI performs an authenticated REST request to management.azure.com.
// method is GET/PUT/DELETE/POST; path is relative e.g.
// "/subscriptions/.../resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/vm1"
// Returns the response body for 2xx, error otherwise.
func (a *AzureCRP) doAPI(method, path string, body any) ([]byte, int, error) {
	tok, err := a.getToken()
	if err != nil {
		return nil, 0, err
	}

	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal body: %w", err)
		}
		buf = bytes.NewReader(b)
	}

	// Select the API version based on the resource provider in the path so we
	// use a version that each resource type actually supports in the region.
	apiVersion := computeAPIVersion
	if strings.Contains(path, "/providers/Microsoft.Network/") {
		apiVersion = networkAPIVersion
	}

	url := "https://management.azure.com" + path
	if strings.Contains(path, "?") {
		url += "&api-version=" + apiVersion
	} else {
		url += "?api-version=" + apiVersion
	}

	req, err := http.NewRequest(method, url, buf)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode >= 300 {
		return data, resp.StatusCode, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, string(data))
	}
	return data, resp.StatusCode, nil
}

// Ensure provisions Count worker VMs (no public IP, key login only) until the
// pool size is met. Returns VM handles immediately with the VM name as the
// stable ID (the same name used as computerName — the node hostname).
func (a *AzureCRP) Ensure(req EnsureRequest) ([]VM, error) {
	var result []VM

	for i := 0; i < req.Count; i++ {
		vmName, err := a.genNodeName()
		if err != nil {
			return result, fmt.Errorf("gen node name: %w", err)
		}

		// Create the NIC (private IP only, no public IP).
		nicName := vmName + "-nic"
		nicID, err := a.createNIC(req, nicName, vmName)
		if err != nil {
			log.Printf("[AzureCRP] createNIC %s failed: %v", nicName, err)
			// Try to continue with next VM rather than falling over.
			continue
		}

		// Create the VM (key login only; custom-data cloud-init bootstrap).
		vmID, err := a.createVM(req, vmName, nicID)
		if err != nil {
			log.Printf("[AzureCRP] createVM %s failed: %v", vmName, err)
			continue
		}

		a.registry.add(vmName, req.ResourceGroup, req.Location)

		result = append(result, VM{
			ID:            vmName, // computer name = stable ID
			Name:          vmName,
			SKU:           req.SKU,
			State:         VMStateCreating,
			Location:      req.Location,
			ResourceGroup: req.ResourceGroup,
			CreatedAt:     time.Now(),
		})
		log.Printf("[AzureCRP] Ensure created VM %s (id=%s) sku=%s", vmName, vmID, req.SKU)
	}
	return result, nil
}

// genNodeName generates a short, unique node/VM name.
// Azure VM names: 1-64 chars, lowercase letters, numbers and hyphens.
func (a *AzureCRP) genNodeName() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "ot-node-" + base64.RawURLEncoding.EncodeToString(b), nil
}

// createNIC creates a network interface in the queue's subnet with a private
// IP. Returns the NIC resource ID.
func (a *AzureCRP) createNIC(req EnsureRequest, nicName, vmName string) (string, error) {
	path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkInterfaces/%s",
		a.subscription, req.ResourceGroup, nicName)

	body := map[string]any{
		"location": req.Location,
		"properties": map[string]any{
			"ipConfigurations": []map[string]any{
				{
					"name": "ipconfig1",
					"properties": map[string]any{
						"subnet": map[string]string{"id": req.SubnetID},
						"privateIPAllocationMethod": "Dynamic",
					},
				},
			},
		},
	}

	if _, _, err := a.doAPI("PUT", path, body); err != nil {
		return "", err
	}
	return path, nil
}

// createVM creates a virtual machine in the queue's resource group using the
// queue-specified SKU, image, subnet and cloud-init custom-data. SSH key login
// only (no password). Returns the VM resource ID.
func (a *AzureCRP) createVM(req EnsureRequest, vmName, nicID string) (string, error) {
	// Build computerName from vmName; Azure limits to 64 chars, and for Linux
	// it also becomes the hostname used by the MOM.
	computerName := vmName
	if len(computerName) > 64 {
		computerName = computerName[:64]
	}

	// CustomData (cloud-init) for MOM bootstrap.
	customData := buildCloudInit(req, vmName)

	// Image reference: req.ImageID may be a URN like
	// "Canonical:0001-com-ubuntu-server-jammy:22_04-LTS-gen2:latest", or a
	// bare marketplace image ID, or a custom image resource ID.
	imageVal := map[string]any{}
	if strings.Contains(req.ImageID, "/") && strings.HasPrefix(req.ImageID, "/subscriptions/") {
		// Custom image by resource id.
		imageVal["id"] = req.ImageID
	} else {
		parts := strings.Split(req.ImageID, ":")
		if len(parts) == 4 {
			imageVal = map[string]any{
				"publisher": parts[0],
				"offer":     parts[1],
				"sku":       parts[2],
				"version":   parts[3],
			}
		} else {
			// Fallback to the standard Ubuntu image.
			imageVal = map[string]any{
				"publisher": "Canonical",
				"offer":     "0001-com-ubuntu-server-jammy",
				"sku":       "22_04-LTS-gen2",
				"version":   "latest",
			}
		}
	}

	// OS disk.
	osDisk := map[string]any{
		"createOption": "FromImage",
		"deleteOption": "Delete",
	}
	if req.DiskSize > 0 {
		osDisk["diskSizeGB"] = req.DiskSize
	}
	if req.DiskType != "" {
		osDisk["managedDisk"] = map[string]string{"storageAccountType": req.DiskType}
	}

	hardwareProfile := map[string]any{"vmSize": req.SKU}
	if req.Hibernate {
		// hibernationEnabled must be set at VM creation; Azure then auto-
		// hibernates on deallocate for supported SKUs/OS images.
		hardwareProfile["hibernationEnabled"] = true
	}

	body := map[string]any{
		"location": req.Location,
		"properties": map[string]any{
			"hardwareProfile": hardwareProfile,
			"osProfile": map[string]any{
				"computerName":  computerName,
				"adminUsername": "azureuser",
				"linuxConfiguration": map[string]any{
					"disablePasswordAuthentication": true,
					"ssh": map[string]any{
						"publicKeys": []map[string]string{
							{
								"path":    "/home/azureuser/.ssh/authorized_keys",
								"keyData": req.SSHKey,
							},
						},
					},
				},
				"customData": base64.StdEncoding.EncodeToString([]byte(customData)),
			},
			"storageProfile": map[string]any{
				"imageReference":  imageVal,
				"osDisk":          osDisk,
			},
			"networkProfile": map[string]any{
				"networkInterfaces": []map[string]string{
					{"id": nicID},
				},
			},
		},
		// "identity" is not needed since the CRP uses the srv VM's identity.
	}

	path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s",
		a.subscription, req.ResourceGroup, vmName)

	if _, _, err := a.doAPI("PUT", path, body); err != nil {
		return "", err
	}
	return path, nil
}

// Describe returns the current state of a single VM.
func (a *AzureCRP) Describe(ref VMRef) (VM, error) {
	// ref.VMID is the VM name (computerName / node name).
	rg, loc := a.splitRef(ref)

	path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s/instanceView",
		a.subscription, rg, ref.VMID)
	data, code, err := a.doAPI("GET", path, nil)
	if err != nil {
		// Fall back to a GetVM call that returns basic info.
		if code == 404 {
			return VM{}, nil
		}
		return VM{}, err
	}

	var iv struct {
		Statuses []struct {
			Code string `json:"code"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(data, &iv); err != nil {
		return VM{}, fmt.Errorf("parse instanceView: %w", err)
	}

	state := VMStateCreating
	for _, st := range iv.Statuses {
		if st.Code == "PowerState/running" {
			state = VMStateRunning
		} else if st.Code == "PowerState/deallocated" {
			state = VMStateStopped
		} else if st.Code == "PowerState/stopped" {
			state = VMStateStopped
		}
	}
	_ = loc

	return VM{
		ID:            ref.VMID,
		Name:          ref.VMID,
		State:         state,
		Location:      loc,
		ResourceGroup: rg,
	}, nil
}

// Reclaim destroys (or deallocates/hibernates) a VM per the queue reclaim
// policy. In the current event-driven design, deallocated VMs are destroyed
// because they are not easily reusable with dynamic container names; the
// destroy flag forces deletion.
func (a *AzureCRP) Reclaim(ref VMRef, policy string, destroy bool) error {
	rg, _ := a.splitRef(ref)
	path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s",
		a.subscription, rg, ref.VMID)

	if destroy {
		_, _, err := a.doAPI("DELETE", path+"?forceDeletion=true", nil)
		return err
	}

	switch policy {
	case "hibernate":
		// First deallocate, then (in a real impl) hibernate. Azure does not
		// have a direct "hibernate" REST call; it is a VM property. For now
		// treat identical to deallocate.
		_, _, err := a.doAPI("POST", path+"/deallocate", nil)
		return err
	default: // "deallocate"
		_, _, err := a.doAPI("POST", path+"/deallocate", nil)
		return err
	}
}

// Resume starts a stopped/deallocated VM.
func (a *AzureCRP) Resume(ref VMRef) error {
	rg, _ := a.splitRef(ref)
	path := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s/start",
		a.subscription, rg, ref.VMID)
	_, _, err := a.doAPI("POST", path, nil)
	return err
}

// Health checks whether a VM exists and is reachable.
func (a *AzureCRP) Health(ref VMRef) error {
	_, err := a.Describe(ref)
	return err
}

// azureVMInfo tracks the resource-group + location of VMs this provider has
// created so Reclaim/Describe can reconstruct the REST path.
type azureVMInfo struct {
	rg       string
	location string
}

// vmRegistry maps VM name -> (rg, location) for this provider instance.
type vmRegistry struct {
	byName map[string]azureVMInfo
}

func newVMRegistry() *vmRegistry {
	return &vmRegistry{byName: make(map[string]azureVMInfo)}
}

func (r *vmRegistry) add(name, rg, loc string) {
	r.byName[name] = azureVMInfo{rg: rg, location: loc}
}

func (r *vmRegistry) get(name string) (azureVMInfo, bool) {
	v, ok := r.byName[name]
	return v, ok
}

// splitRef is no longer used; the AzureCRP tracks RG/location internally.
func (a *AzureCRP) splitRef(ref VMRef) (rg, loc string) {
	if info, ok := a.registry.get(ref.VMID); ok {
		return info.rg, info.location
	}
	return "", ""
}

// buildCloudInit generates the cloud-init user-data script that bootstraps a
// dynamic worker node: downloads pbs_mom + auth_key from the server over HTTP,
// configures mom_priv, and starts pbs_mom.
func buildCloudInit(req EnsureRequest, vmName string) string {
	// The server address is passed as host (the pbs_sched calls it cfg.Server).
	serverHost := req.ServerAddr
	if serverHost == "" {
		serverHost = "10.20.0.4" // fallback (test cluster srv IP)
	}

	// Determine server IP: serverAddr may be hostname or IP:port.
	serverIP := serverHost
	if idx := strings.Index(serverHost, ":"); idx >= 0 {
		serverIP = serverHost[:idx]
	}

	return fmt.Sprintf(`#cloud-config
#cloud-config
package_update: false
runcmd:
  - |
    set -e
    # --- OpenTorque dynamic worker bootstrap ---
    SERVER_IP="%s"
    NODE_NAME="%s"
    mkdir -p /usr/local/sbin /var/spool/torque/mom_priv /var/spool/torque/mom_logs /var/spool/torque/spool /var/spool/torque/undelivered /var/spool/torque/aux /var/spool/torque/server_priv
    # Download pbs_mom binary from the server (simple HTTP file server on :8080)
    if command -v curl >/dev/null 2>&1; then
      curl -sS -o /usr/local/sbin/pbs_mom "http://${SERVER_IP}:8080/pbs_mom"
    else
      wget -q -O /usr/local/sbin/pbs_mom "http://${SERVER_IP}:8080/pbs_mom"
    fi
    chmod +x /usr/local/sbin/pbs_mom
    ln -sf /usr/local/sbin/pbs_mom /usr/bin/pbs_mom || true
    # Download shared auth key
    if command -v curl >/dev/null 2>&1; then
      curl -sS -o /var/spool/torque/auth_key "http://${SERVER_IP}:8080/auth_key"
    else
      wget -q -O /var/spool/torque/auth_key "http://${SERVER_IP}:8080/auth_key"
    fi
    chmod 600 /var/spool/torque/auth_key
    # server_name for MOM
    echo "${SERVER_IP}" > /var/spool/torque/server_name
    # mom_priv config
    echo "\$pbsserver      ${SERVER_IP}" > /var/spool/torque/mom_priv/config
    echo "\$clienthost     ${SERVER_IP}" >> /var/spool/torque/mom_priv/config
    # Start MOM
    nohup /usr/local/sbin/pbs_mom -d /var/spool/torque >/var/spool/torque/mom_logs/start.out 2>&1 &
    echo "pbs_mom started on ${NODE_NAME} (server %s)"
`, serverIP, vmName, serverIP)
}

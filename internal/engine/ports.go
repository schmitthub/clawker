package engine

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/docker/go-connections/nat"
)

// ParsePortSpecs parses Docker-style port specifications into PortMap and PortSet.
// Supported formats:
//   - containerPort (random host port)
//   - hostPort:containerPort
//   - hostIP:hostPort:containerPort
//   - port-range:port-range (e.g., 24280-24290:24280-24290)
//   - Any of the above with /tcp or /udp suffix (default: tcp)
//
// Examples:
//
//	"8080" -> random host port to container 8080/tcp
//	"8080:8080" -> host 8080 to container 8080/tcp
//	"127.0.0.1:8080:8080" -> localhost:8080 to container 8080/tcp
//	"8080:8080/udp" -> host 8080 to container 8080/udp
//	"24280-24290:24280-24290" -> port range mapping
func ParsePortSpecs(specs []string) (nat.PortMap, nat.PortSet, error) {
	portMap := nat.PortMap{}
	portSet := nat.PortSet{}

	for _, spec := range specs {
		if err := parsePortSpec(spec, portMap, portSet); err != nil {
			return nil, nil, fmt.Errorf("invalid port specification %q: %w", spec, err)
		}
	}

	return portMap, portSet, nil
}

// parsePortSpec parses a single port specification and adds it to the maps.
func parsePortSpec(spec string, portMap nat.PortMap, portSet nat.PortSet) error {
	// Extract protocol suffix if present
	proto := "tcp"
	if idx := strings.LastIndex(spec, "/"); idx != -1 {
		proto = spec[idx+1:]
		spec = spec[:idx]
		if proto != "tcp" && proto != "udp" {
			return fmt.Errorf("invalid protocol %q (must be tcp or udp)", proto)
		}
	}

	parts := strings.Split(spec, ":")
	var hostIP, hostPort, containerPort string

	switch len(parts) {
	case 1:
		// containerPort only (random host port)
		containerPort = parts[0]
		hostPort = ""
	case 2:
		// hostPort:containerPort
		hostPort = parts[0]
		containerPort = parts[1]
	case 3:
		// hostIP:hostPort:containerPort
		hostIP = parts[0]
		hostPort = parts[1]
		containerPort = parts[2]
	default:
		return fmt.Errorf("invalid format (expected [hostIP:][hostPort:]containerPort)")
	}

	// Check if this is a port range
	if strings.Contains(containerPort, "-") {
		return parsePortRange(hostIP, hostPort, containerPort, proto, portMap, portSet)
	}

	// Validate container port
	if _, err := strconv.Atoi(containerPort); err != nil {
		return fmt.Errorf("invalid container port %q", containerPort)
	}

	// Validate host port if specified
	if hostPort != "" {
		if _, err := strconv.Atoi(hostPort); err != nil {
			return fmt.Errorf("invalid host port %q", hostPort)
		}
	}

	// Create the port binding
	port := nat.Port(containerPort + "/" + proto)
	portSet[port] = struct{}{}
	portMap[port] = append(portMap[port], nat.PortBinding{
		HostIP:   hostIP,
		HostPort: hostPort,
	})

	return nil
}

// parsePortRange handles port range specifications like "24280-24290:24280-24290".
func parsePortRange(hostIP, hostPortRange, containerPortRange, proto string, portMap nat.PortMap, portSet nat.PortSet) error {
	// Parse container port range
	containerStart, containerEnd, err := parseRange(containerPortRange)
	if err != nil {
		return fmt.Errorf("invalid container port range: %w", err)
	}

	// Parse host port range (if specified)
	var hostStart, hostEnd int
	if hostPortRange != "" {
		hostStart, hostEnd, err = parseRange(hostPortRange)
		if err != nil {
			return fmt.Errorf("invalid host port range: %w", err)
		}

		// Ranges must be the same size
		if (containerEnd - containerStart) != (hostEnd - hostStart) {
			return fmt.Errorf("host and container port ranges must be the same size")
		}
	}

	// Create bindings for each port in the range
	for i := 0; i <= containerEnd-containerStart; i++ {
		containerPort := containerStart + i
		port := nat.Port(fmt.Sprintf("%d/%s", containerPort, proto))
		portSet[port] = struct{}{}

		binding := nat.PortBinding{HostIP: hostIP}
		if hostPortRange != "" {
			binding.HostPort = strconv.Itoa(hostStart + i)
		}
		portMap[port] = append(portMap[port], binding)
	}

	return nil
}

// parseRange parses a port range string like "24280-24290" into start and end ports.
func parseRange(s string) (start, end int, err error) {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid range format %q (expected start-end)", s)
	}

	start, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start port %q", parts[0])
	}

	end, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end port %q", parts[1])
	}

	if start > end {
		return 0, 0, fmt.Errorf("start port %d must be <= end port %d", start, end)
	}

	if start < 1 || end > 65535 {
		return 0, 0, fmt.Errorf("ports must be between 1 and 65535")
	}

	return start, end, nil
}

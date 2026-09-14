package store

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strings"
)

// Redis error replies are remote input and can echo authentication arguments.
// Classify them without forwarding arbitrary server text to logs or the UI.
func safeRedisConnectionError(connectionError error) error {
	var certificateError *tls.CertificateVerificationError
	if errors.As(connectionError, &certificateError) {
		return errors.New("redis TLS certificate verification failed")
	}
	var dnsError *net.DNSError
	if errors.As(connectionError, &dnsError) {
		return errors.New("redis host could not be resolved")
	}
	var networkError net.Error
	if errors.Is(connectionError, context.DeadlineExceeded) ||
		(errors.As(connectionError, &networkError) && networkError.Timeout()) {
		return errors.New("redis connection timed out; check service availability and network access")
	}
	message := strings.ToUpper(connectionError.Error())
	if strings.HasPrefix(message, "WRONGPASS") || strings.HasPrefix(message, "NOAUTH") {
		return errors.New("redis authentication failed; check username and password")
	}
	if strings.HasPrefix(message, "NOPERM") {
		return errors.New("redis user is not permitted to run the connection check")
	}
	return errors.New("redis connection failed; check service state, address, credentials and TLS")
}

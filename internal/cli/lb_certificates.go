package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

// certificateCommand manages the TLS certificates TERMINATED_HTTPS listeners
// serve.
//
// Import reads PEM files and sends them once; the private key is never printed
// and cannot be read back. A certificate is immutable: renewing one means
// importing the new certificate and pointing the listener at it.
func certificateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "certificate",
		Aliases: []string{"certificates", "cert", "certs"},
		Short:   "TLS certificates for TERMINATED_HTTPS listeners",
		Long: `TLS certificates for TERMINATED_HTTPS listeners.

A certificate is imported into the project, then named by its ID with
"nemau lb listener create --protocol TERMINATED_HTTPS --tls-certificate <id>".
The private key is sealed by the platform and never shown again.

A certificate is immutable. To renew one without downtime:

  nemau lb certificate import --name www-2027 --certificate fullchain.pem --private-key privkey.pem
  nemau lb listener update <listener-id> --tls-certificate <new-id>
  nemau lb certificate delete <old-id>`,
	}
	cmd.AddCommand(certificateImportCommand(), certificateListCommand(), certificateGetCommand(), certificateDeleteCommand())
	return cmd
}

func certificateImportCommand() *cobra.Command {
	var name, certificatePath, keyPath, chainPath string
	cmd := &cobra.Command{
		Use:     "import",
		Aliases: []string{"create"},
		Short:   "Import a certificate and its private key from PEM files",
		Long: `Import a certificate and its private key from PEM files.

--certificate holds the leaf, optionally followed by its chain (a fullchain.pem);
--chain adds intermediates kept in a separate file. The key must not be
passphrase-protected. Accepted keys: RSA 2048 to 4096 bits, ECDSA P-256 and
P-384.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			req := nemau.ImportCertificateRequest{Name: name}
			var err error
			if req.Certificate, err = readPEMFile("--certificate", certificatePath); err != nil {
				return err
			}
			if req.PrivateKey, err = readPEMFile("--private-key", keyPath); err != nil {
				return err
			}
			if chainPath != "" {
				if req.Chain, err = readPEMFile("--chain", chainPath); err != nil {
					return err
				}
			}
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			cert, err := c.LoadBalancer.ImportCertificate(ctx, req)
			if err != nil {
				return err
			}
			return renderCertificate(cmd, cert)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "certificate name, unique in the project (required)")
	cmd.Flags().StringVar(&certificatePath, "certificate", "", "PEM file with the certificate, optionally followed by its chain (required)")
	cmd.Flags().StringVar(&keyPath, "private-key", "", "PEM file with the unencrypted private key (required)")
	cmd.Flags().StringVar(&chainPath, "chain", "", "PEM file with intermediate certificates")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("certificate")
	_ = cmd.MarkFlagRequired("private-key")
	return cmd
}

// readPEMFile reads a PEM file named by a flag. The error names the flag and
// the path, never the content.
func readPEMFile(flag, path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", flag, err)
	}
	if !strings.Contains(string(raw), "-----BEGIN ") {
		return "", fmt.Errorf("%s: %s is not a PEM file", flag, path)
	}
	return string(raw), nil
}

func certificateListCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the project's certificates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			items, err := c.LoadBalancer.ListCertificates(ctx)
			if err != nil {
				return err
			}
			v := view{columns: []string{"ID", "NAME", "DOMAINS", "ISSUER", "EXPIRES", "USED BY"}, data: items}
			for _, it := range items {
				v.rows = append(v.rows, []string{it.ID, it.Name, joinLimit(it.Domains, 3), dash(it.Issuer),
					certificateExpiry(it.NotAfter), certificateUsers(it.UsedBy)})
				v.ids = append(v.ids, it.ID)
			}
			return render(cmd, v)
		},
	}
}

func certificateGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one certificate",
		Args:  requireArgs(1, "nemau lb certificate get <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			cert, err := c.LoadBalancer.GetCertificate(ctx, args[0])
			if err != nil {
				return err
			}
			return renderCertificate(cmd, cert)
		},
	}
}

func certificateDeleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a certificate no listener serves",
		Args:    requireArgs(1, "nemau lb certificate delete <id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			if err := c.LoadBalancer.DeleteCertificate(ctx, args[0]); err != nil {
				if nemau.IsCertificateInUse(err) {
					return fmt.Errorf("%w\npoint the listener at another certificate (nemau lb listener update <id> --tls-certificate <id>) or delete it first", err)
				}
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "certificate deleted:", args[0])
			return nil
		},
	}
}

func renderCertificate(cmd *cobra.Command, cert *nemau.Certificate) error {
	rows := [][]string{
		{"id", cert.ID},
		{"name", cert.Name},
		{"subject", dash(cert.Subject)},
		{"domains", joinLimit(cert.Domains, 20)},
		{"issuer", dash(cert.Issuer)},
		{"valid from", certificateDate(cert.NotBefore)},
		{"expires", certificateExpiry(cert.NotAfter)},
		{"key", dash(cert.KeyAlgorithm)},
		{"chain", fmt.Sprintf("%d intermediate(s)", cert.ChainLength)},
		{"fingerprint (SHA-256)", dash(cert.FingerprintSHA256)},
		{"used by", certificateUsers(cert.UsedBy)},
	}
	return render(cmd, view{columns: []string{"FIELD", "VALUE"}, rows: rows, data: cert, id: cert.ID})
}

func certificateDate(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02")
}

// certificateExpiry is the expiry date, flagged when it is close or past: a
// listener serving an expired certificate fails every handshake.
func certificateExpiry(notAfter time.Time) string {
	if notAfter.IsZero() {
		return "-"
	}
	date := certificateDate(notAfter)
	switch remaining := time.Until(notAfter); {
	case remaining <= 0:
		return date + " (expired)"
	case remaining < 30*24*time.Hour:
		return fmt.Sprintf("%s (in %d days)", date, int(remaining.Hours()/24))
	}
	return date
}

func certificateUsers(uses []nemau.CertificateUse) string {
	if len(uses) == 0 {
		return "-"
	}
	names := make([]string, 0, len(uses))
	for _, use := range uses {
		names = append(names, use.ListenerName)
	}
	return joinLimit(names, 3)
}

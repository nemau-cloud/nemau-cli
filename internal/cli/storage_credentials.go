package cli

import (
	"errors"
	"fmt"
	"time"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
)

// credentialsCommand manages the S3 keys of one container.
//
// With these keys, `aws s3 sync` and `rclone sync` work over a whole bucket,
// where a presigned URL covers one object. They are the main way to copy data
// out of the platform with standard S3 tools.
func credentialsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "credentials",
		Aliases: []string{"creds"},
		Short:   "S3 keys of one container, for rclone, aws-cli and s3cmd",
		Long: `S3 keys of one container, for rclone, aws-cli and s3cmd.

A key belongs to one container and is either read-only or read-write: it never
sees another container and never changes container settings. A container holds
several keys, each revoked on its own, and deleting the container revokes them.

The secret is shown once. The platform does not keep it and cannot recover it.

S3 tools address the container by its S3 bucket name, which differs from the
container name: the object store knows each container under an opaque name of
its own.
"create" prints it (s3_bucket, and NEMAU_S3_BUCKET with --format env), and
"nemau storage container list" shows it for every container.

A key stays valid until it is revoked, unless you give it an expiry with
--expires-in or --expires-at. A presigned URL, by contrast, expires on its own
within an hour. Create and revoke are both audited.

These commands need permission to manage storage credentials
(storage:ManageCredentials).`,
	}
	cmd.AddCommand(credentialsCreateCommand(), credentialsListCommand(), credentialsRevokeCommand())
	return cmd
}

// accessFromFlag maps the flag's spelling to the API's.
func accessFromFlag(value string) (nemau.CredentialAccess, error) {
	switch value {
	case "read-only":
		return nemau.CredentialReadOnly, nil
	case "read-write":
		return nemau.CredentialReadWrite, nil
	}
	return "", fmt.Errorf("--access must be read-only or read-write, got %q", value)
}

// withCredentialHint tells the user what to do when the platform refused to
// create a key. The platform's message is kept; the hint names the next command.
func withCredentialHint(err error, bucket string) error {
	switch {
	case nemau.IsCredentialLimit(err):
		revoke := fmt.Sprintf(
			"revoke an unused key: nemau storage credentials revoke %s <access-key-id> (see them with: nemau storage credentials list %s)",
			bucket, bucket)
		return &hintedError{err: err, hint: credentialLimitText(err) + revoke}
	case nemau.IsOrganizationReadOnly(err):
		return &hintedError{err: err, hint: "the organization is read-only: only --access read-only keys can be created"}
	}
	return err
}

// credentialLimitText states the limit when the platform reported it:
// "key limit reached (10 per bucket): ". Without a number it states no limit.
func credentialLimitText(err error) string {
	limit, scope, _ := nemau.CredentialLimit(err)
	if limit <= 0 {
		return "key limit reached: "
	}
	if scope == "" {
		return fmt.Sprintf("key limit reached (%d): ", limit)
	}
	return fmt.Sprintf("key limit reached (%d per %s): ", limit, scope)
}

func credentialsCreateCommand() *cobra.Command {
	var (
		format    string
		access    string
		expiresIn time.Duration
		expiresAt string
	)
	cmd := &cobra.Command{
		Use:   "create <container>",
		Short: "Create an S3 key scoped to one container",
		Args:  requireArgs(1, "nemau storage credentials create <container> --access read-only|read-write"),
		RunE: func(cmd *cobra.Command, args []string) error {
			bucket := args[0]
			level, err := accessFromFlag(access)
			if err != nil {
				return err
			}
			req := nemau.CreateCredentialRequest{Access: level}
			switch {
			case expiresIn < 0:
				return errors.New("--expires-in must be positive")
			case expiresIn > 0:
				req.ExpiresAt = time.Now().Add(expiresIn)
			case expiresAt != "":
				when, perr := time.Parse(time.RFC3339, expiresAt)
				if perr != nil {
					return fmt.Errorf("--expires-at must be an RFC 3339 time such as 2026-12-31T23:59:00Z: %w", perr)
				}
				req.ExpiresAt = when
			}
			switch format {
			case "", "env", "rclone":
			default:
				return fmt.Errorf("unknown format %q (env, rclone)", format)
			}

			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			credential, err := c.Storage.CreateCredential(ctx, bucket, req)
			if err != nil {
				return withCredentialHint(err, bucket)
			}

			switch format {
			case "env":
				printCredentialEnv(cmd, credential)
				return nil
			case "rclone":
				printCredentialRclone(cmd, credential)
				return nil
			}

			// The secret is on stdout exactly once; the warning goes to stderr so
			// a redirection captures the value without the prose.
			fmt.Fprintln(cmd.ErrOrStderr(),
				"the secret is shown here only: the platform does not keep it")
			if credential.Endpoint == "" {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"this environment has no reachable S3 endpoint to show: the key cannot be used from outside")
			}
			if credential.S3Bucket != "" {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"S3 tools address the bucket %s, not the container name %s\n", credential.S3Bucket, credential.Container)
			}
			return render(cmd, view{
				columns: []string{"FIELD", "VALUE"},
				rows: [][]string{
					{"access_key_id", credential.AccessKeyID},
					{"secret_access_key", credential.SecretAccessKey},
					{"container", credential.Container},
					{"s3_bucket", dash(credential.S3Bucket)},
					{"access", string(credential.Access)},
					{"endpoint", credential.Endpoint},
					{"region", credential.Region},
					{"expires", expiryText(credential.ExpiresAt)},
				},
				data: credential,
				id:   credential.AccessKeyID,
			})
		},
	}
	cmd.Flags().StringVar(&access, "access", "", "what the key may do: read-only or read-write (required)")
	_ = cmd.MarkFlagRequired("access")
	cmd.Flags().DurationVar(&expiresIn, "expires-in", 0, "revoke the key after this long, e.g. 24h (default: never)")
	cmd.Flags().StringVar(&expiresAt, "expires-at", "", "revoke the key at this RFC 3339 time (default: never)")
	cmd.MarkFlagsMutuallyExclusive("expires-in", "expires-at")
	cmd.Flags().StringVar(&format, "format", "",
		"ready-to-use output: env (shell exports) or rclone (a config section)")
	return cmd
}

func expiryText(expires *time.Time) string {
	if expires == nil {
		return "never"
	}
	return expires.Format(time.RFC3339)
}

// unknownBucket stands in for the S3 bucket name in a hint when the platform
// did not report it. The container name is not used instead because the store
// does not accept it, so the printed command would fail.
const unknownBucket = "<s3-bucket>"

// s3Bucket is the bucket name S3 tools must be given for this key.
func s3Bucket(credential *nemau.IssuedCredential) string {
	if credential.S3Bucket == "" {
		return unknownBucket
	}
	return credential.S3Bucket
}

// printCredentialEnv emits the exact variables aws-cli and s3cmd read, plus
// NEMAU_S3_BUCKET: the bucket to address, which is not the container name.
func printCredentialEnv(cmd *cobra.Command, credential *nemau.IssuedCredential) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "export AWS_ACCESS_KEY_ID=%s\n", credential.AccessKeyID)
	fmt.Fprintf(out, "export AWS_SECRET_ACCESS_KEY=%s\n", credential.SecretAccessKey)
	fmt.Fprintf(out, "export AWS_DEFAULT_REGION=%s\n", credential.Region)
	if credential.S3Bucket != "" {
		fmt.Fprintf(out, "export NEMAU_S3_BUCKET=%s\n", credential.S3Bucket)
	}
	if credential.Endpoint != "" {
		fmt.Fprintf(out, "export AWS_ENDPOINT_URL=%s\n", credential.Endpoint)
		fmt.Fprintf(cmd.ErrOrStderr(),
			"\n# aws --endpoint-url \"$AWS_ENDPOINT_URL\" s3 sync s3://%s ./backup\n", s3Bucket(credential))
		return
	}
	fmt.Fprintln(cmd.ErrOrStderr(),
		"\n# no reachable S3 endpoint in this environment: AWS_ENDPOINT_URL is not set")
}

// printCredentialRclone emits a ready-to-paste remote definition.
//
// provider = Other and force_path_style are both required: rclone's S3 backend
// changes its behaviour per provider, and its AWS defaults assume virtual-host
// addressing, which this store does not use.
func printCredentialRclone(cmd *cobra.Command, credential *nemau.IssuedCredential) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, `[nemau]
type = s3
provider = Other
access_key_id = %s
secret_access_key = %s
endpoint = %s
region = %s
force_path_style = true
`, credential.AccessKeyID, credential.SecretAccessKey, credential.Endpoint, credential.Region)
	fmt.Fprintf(cmd.ErrOrStderr(),
		"\n# rclone sync nemau:%s ./backup --progress\n", s3Bucket(credential))
}

func credentialsListCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "list <container>",
		Aliases: []string{"ls"},
		Short:   "List the active S3 keys of a container",
		Args:    requireArgs(1, "nemau storage credentials list <container>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			credentials, err := c.Storage.ListCredentials(ctx, args[0])
			if err != nil {
				return err
			}
			v := view{columns: []string{"ACCESS KEY ID", "ACCESS", "ISSUED", "EXPIRES"}, data: credentials}
			for _, credential := range credentials {
				created := credential.CreatedAt
				v.rows = append(v.rows, []string{
					credential.AccessKeyID, string(credential.Access), ago(&created), expiryText(credential.ExpiresAt),
				})
				v.ids = append(v.ids, credential.AccessKeyID)
			}
			return render(cmd, v)
		},
	}
}

func credentialsRevokeCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "revoke <container> <access-key-id>",
		Aliases: []string{"rm", "delete"},
		Short:   "Revoke an S3 key of a container",
		Args:    requireArgs(2, "nemau storage credentials revoke <container> <access-key-id>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			if err := c.Storage.RevokeCredential(ctx, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "key revoked:", args[1])
			return nil
		},
	}
}

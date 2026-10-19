package cli

import (
	"context"
	"errors"
	"strings"

	nemau "github.com/nemau-cloud/nemau-sdk-go"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// updateFlags declares the flags an `update` subcommand accepts, and turns the
// ones the user actually typed into a nemau.Update.
//
// It tells "not given" apart from "given empty": `--description ""` clears a
// description, while omitting the flag leaves it alone. The value alone cannot
// show the difference, so the build step asks cobra which flags were changed.
type updateFlags struct {
	name            string
	description     string
	lbAlgorithm     string
	expectedCodes   string
	tlsCertificate  string
	email           string
	connectionLimit int
	weight          int
	delay           int
	timeout         int
	maxRetries      int
	retentionDays   int
	ttl             int
	versioning      bool
}

// Flag names, kept in one place so the register and build steps stay in sync.
const (
	flagName            = "name"
	flagDescription     = "description"
	flagLBAlgorithm     = "algorithm"
	flagConnectionLimit = "connection-limit"
	flagWeight          = "weight"
	flagDelay           = "delay"
	flagProbeTimeout    = "probe-timeout"
	flagMaxRetries      = "max-retries"
	flagExpectedCodes   = "expected-codes"
	flagTLSCertificate  = "tls-certificate"
	flagVersioning      = "versioning"
	flagRetentionDays   = "retention-days"
	flagEmail           = "email"
	flagTTL             = "ttl"
)

// register adds the named flags to a command. Each `update` subcommand asks for
// the subset its resource accepts, so `--weight` never appears on a command
// that cannot use it.
func (u *updateFlags) register(cmd *cobra.Command, names ...string) {
	f := cmd.Flags()
	for _, name := range names {
		switch name {
		case flagName:
			f.StringVar(&u.name, flagName, "", "new name")
		case flagDescription:
			f.StringVar(&u.description, flagDescription, "", "new description (pass an empty string to clear it)")
		case flagLBAlgorithm:
			f.StringVar(&u.lbAlgorithm, flagLBAlgorithm, "", "ROUND_ROBIN, LEAST_CONNECTIONS or SOURCE_IP")
		case flagConnectionLimit:
			f.IntVar(&u.connectionLimit, flagConnectionLimit, 0, "connection limit")
		case flagWeight:
			f.IntVar(&u.weight, flagWeight, 0, "weight in the balancing")
		case flagDelay:
			f.IntVar(&u.delay, flagDelay, 0, "interval between two probes, in seconds")
		case flagProbeTimeout:
			f.IntVar(&u.timeout, flagProbeTimeout, 0, "timeout for one probe, in seconds")
		case flagMaxRetries:
			f.IntVar(&u.maxRetries, flagMaxRetries, 0, "consecutive failures before taking the backend out")
		case flagExpectedCodes:
			f.StringVar(&u.expectedCodes, flagExpectedCodes, "", "expected HTTP codes, for example 200-299")
		case flagTLSCertificate:
			f.StringVar(&u.tlsCertificate, flagTLSCertificate, "", "ID of the certificate a TERMINATED_HTTPS listener serves")
		case flagVersioning:
			f.BoolVar(&u.versioning, flagVersioning, false, "keep previous versions of objects")
		case flagRetentionDays:
			f.IntVar(&u.retentionDays, flagRetentionDays, 0, "how long objects stay immutable, in days (can only be raised)")
		case flagEmail:
			f.StringVar(&u.email, flagEmail, "", "SOA contact")
		case flagTTL:
			f.IntVar(&u.ttl, flagTTL, 0, "default TTL in seconds")
		}
	}
}

// build turns the flags the user typed into an update, or reports that none
// were.
func (u *updateFlags) build(cmd *cobra.Command) (nemau.Update, error) {
	update := nemau.Update{}
	changed := cmd.Flags().Changed

	if changed(flagName) {
		update.Name = nemau.Set(u.name)
	}
	if changed(flagDescription) {
		update.Description = nemau.Set(u.description)
	}
	if changed(flagLBAlgorithm) {
		update.LBAlgorithm = nemau.Set(u.lbAlgorithm)
	}
	if changed(flagConnectionLimit) {
		update.ConnectionLimit = nemau.Set(u.connectionLimit)
	}
	if changed(flagWeight) {
		update.Weight = nemau.Set(u.weight)
	}
	if changed(flagDelay) {
		update.Delay = nemau.Set(u.delay)
	}
	if changed(flagProbeTimeout) {
		update.Timeout = nemau.Set(u.timeout)
	}
	if changed(flagMaxRetries) {
		update.MaxRetries = nemau.Set(u.maxRetries)
	}
	if changed(flagExpectedCodes) {
		update.ExpectedCodes = nemau.Set(u.expectedCodes)
	}
	if changed(flagTLSCertificate) {
		update.TLSCertificateRef = nemau.Set(u.tlsCertificate)
	}
	if changed(flagVersioning) {
		update.Versioning = nemau.Set(u.versioning)
	}
	if changed(flagRetentionDays) {
		update.RetentionDays = nemau.Set(u.retentionDays)
	}
	if changed(flagEmail) {
		update.Email = nemau.Set(u.email)
	}
	if changed(flagTTL) {
		update.TTL = nemau.Set(u.ttl)
	}

	if update == (nemau.Update{}) {
		return update, errNoUpdateFlag(cmd)
	}
	return update, nil
}

// errNoUpdateFlag names the flags the command does accept, so the user is not
// left to guess which ones exist.
func errNoUpdateFlag(cmd *cobra.Command) error {
	var names []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" {
			return
		}
		names = append(names, "--"+f.Name)
	})
	return errors.New("nothing to update; pass one of " + strings.Join(names, ", "))
}

// updateCommand builds an `update` subcommand.
//
// Every update command takes an id and the flags the user typed, submits the
// change and waits for it. Sharing this code keeps the flag handling and the
// "given versus empty" rule the same for every resource.
func updateCommand(use, short string, flags []string, usage string, argCount int,
	call func(*nemau.Client, context.Context, *updateFlags, nemau.Update, []string) (*nemau.TaskRef, error),
) *cobra.Command {
	values := &updateFlags{}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  requireArgs(argCount, usage),
		RunE: func(cmd *cobra.Command, args []string) error {
			update, err := values.build(cmd)
			if err != nil {
				return err
			}
			c, ctx, cancel, err := runCtx(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			ref, err := call(c, ctx, values, update, args)
			if err != nil {
				return err
			}
			return finish(cmd, ctx, ref)
		},
	}
	values.register(cmd, flags...)
	return cmd
}

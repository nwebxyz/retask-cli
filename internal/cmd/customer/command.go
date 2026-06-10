// internal/cmd/customer/command.go
package customer

import (
	"context"
	"fmt"
	"strings"

	connectrpc "connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/nwebxyz/retask-cli/internal/auth"
	"github.com/nwebxyz/retask-cli/internal/client"
	"github.com/nwebxyz/retask-cli/internal/config"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/output"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
	customerv1 "github.com/nwebxyz/retask-cli/proto-gen/customer/v1"
	customerv1connect "github.com/nwebxyz/retask-cli/proto-gen/customer/v1/customerv1connect"
)

// NewCommand returns the top-level "customer" cobra command.
func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "customer",
		Short: "Manage customers and your own profile",
	}
	cmd.AddCommand(
		newProfileCommand(gf),
		newListCommand(gf),
		newGetCommand(gf),
	)
	return cmd
}

// connect resolves credentials and returns a CustomerServiceClient plus a
// close function that must be deferred by the caller.
func connect(gf *flags.Global) (customerv1connect.CustomerServiceClient, func(), error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, nil, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return nil, nil, err
	}
	httpClient := client.New(jwt, gf.Insecure)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return customerv1connect.NewCustomerServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
}

// ── customer profile ──────────────────────────────────────────────────────────

func newProfileCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage your own customer profile",
	}
	cmd.AddCommand(
		newProfileGetCommand(gf),
		newProfileSetCommand(gf),
	)
	return cmd
}

func newProfileGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Get your customer profile",
		Long: `Fetch the customer profile of the authenticated user.

Usage example:
  retask customer profile get
  retask customer profile get --pretty

Output fields: customer_id, name, email, timezone, appearance_settings, notification_settings`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetMyProfile(context.Background(), connectrpc.NewRequest(&commonv1.Empty{}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
}

func newProfileSetCommand(gf *flags.Global) *cobra.Command {
	var name, email, timezone, theme string
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Update your customer profile",
		Long: `Update one or more fields on the authenticated user's customer profile.

Only explicitly provided flags are sent; omitted flags keep the current server value.

Usage example:
  retask customer profile set --name "Jane Doe"
  retask customer profile set --timezone "America/New_York"
  retask customer profile set --theme THEME_PREFERENCE_DARK

Flags:
  --name string      Display name
  --email string     Email address
  --timezone string  IANA timezone (e.g. America/New_York)
  --theme string     Theme: THEME_PREFERENCE_LIGHT, THEME_PREFERENCE_DARK, THEME_PREFERENCE_SYSTEM

Output fields: customer_id`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("theme") {
				upper := strings.ToUpper(theme)
				if _, ok := customerv1.AppearanceSettings_ThemePreference_value[upper]; !ok {
					return fmt.Errorf("invalid --theme %q. Valid values: THEME_PREFERENCE_LIGHT, THEME_PREFERENCE_DARK, THEME_PREFERENCE_SYSTEM", theme)
				}
				theme = upper
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			// Fetch existing profile to preserve unset fields.
			existingResp, err := svc.GetMyProfile(context.Background(), connectrpc.NewRequest(&commonv1.Empty{}))
			if err != nil {
				return err
			}

			if cmd.Flags().Changed("name") {
				existingResp.Msg.Name = name
			}
			if cmd.Flags().Changed("email") {
				existingResp.Msg.Email = email
			}
			if cmd.Flags().Changed("timezone") {
				existingResp.Msg.Timezone = timezone
			}
			if cmd.Flags().Changed("theme") {
				v := customerv1.AppearanceSettings_ThemePreference_value[theme]
				if existingResp.Msg.AppearanceSettings == nil {
					existingResp.Msg.AppearanceSettings = &customerv1.AppearanceSettings{}
				}
				existingResp.Msg.AppearanceSettings.Theme = customerv1.AppearanceSettings_ThemePreference(v)
			}

			resp, err := svc.SetMyProfile(context.Background(), connectrpc.NewRequest(existingResp.Msg))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"customer_id": resp.Msg.Id})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Display name")
	cmd.Flags().StringVar(&email, "email", "", "Email address")
	cmd.Flags().StringVar(&timezone, "timezone", "", "IANA timezone (e.g. America/New_York)")
	cmd.Flags().StringVar(&theme, "theme", "", "Theme: THEME_PREFERENCE_LIGHT, THEME_PREFERENCE_DARK, THEME_PREFERENCE_SYSTEM")
	return cmd
}

// ── customer list ─────────────────────────────────────────────────────────────

func newListCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all customers",
		Long: `List all customers (admin use).

Usage example:
  retask customer list
  retask customer list --pretty

Output fields: customer_id, name, email, timezone, created_at, updated_at`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetCustomers(context.Background(), connectrpc.NewRequest(&customerv1.CustomersRequest{}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Customers)
		},
	}
}

// ── customer get ──────────────────────────────────────────────────────────────

func newGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get <customer-id>",
		Short: "Get a customer by ID",
		Long: `Fetch a single customer by their ID (admin use).

Usage example:
  retask customer get cust_abc123
  retask customer get cust_abc123 --pretty

Output fields: customer_id, name, email, timezone, created_at, updated_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetCustomer(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
}

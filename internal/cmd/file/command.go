// internal/cmd/file/command.go
package file

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"
	"nweb.xyz/retask-cli/internal/auth"
	"nweb.xyz/retask-cli/internal/client"
	"nweb.xyz/retask-cli/internal/config"
	"nweb.xyz/retask-cli/internal/flags"
	"nweb.xyz/retask-cli/internal/output"
	commonv1 "nweb.xyz/retask-cli/proto-gen/common/v1"
	filev1 "nweb.xyz/retask-cli/proto-gen/file/v1"
)

// NewCommand returns the top-level "file" cobra command.
func NewCommand(gf *flags.Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file",
		Short: "Manage files",
	}
	cmd.AddCommand(
		newListCommand(gf),
		newGetCommand(gf),
		newDeleteCommand(gf),
		newSignedURLCommand(gf),
	)
	return cmd
}

// connect resolves credentials and returns a FileServiceClient plus a
// close function that must be deferred by the caller.
func connect(gf *flags.Global) (filev1.FileServiceClient, func(), error) {
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
	conn, err := client.New(profile.Endpoint, jwt, gf.Insecure)
	if err != nil {
		return nil, nil, err
	}
	return filev1.NewFileServiceClient(conn), func() { conn.Close() }, nil
}

// ── file list ──────────────────────────────────────────────────────────────

func newListCommand(gf *flags.Global) *cobra.Command {
	var projectID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List files",
		Long: `List files, optionally filtered by project.

Usage examples:
  retask file list
  retask file list --project-id proj_abc123
  retask file list --pretty

Flags:
  --project-id string  Filter files by project ID

Output fields: file_id, project_id, file_name, mime_type, bytes, created_at`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()

			req := &filev1.FilesRequest{}
			if projectID != "" {
				req.Filter = &filev1.FilesRequest_Filter{
					ProjectId: projectID,
				}
			}

			resp, err := svc.GetFiles(context.Background(), req)
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Files)
		},
	}
	cmd.Flags().StringVar(&projectID, "project-id", "", "Filter files by project ID")
	return cmd
}

// ── file get ───────────────────────────────────────────────────────────────

func newGetCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a file by ID",
		Long: `Fetch a single file by its ID.

Usage example:
  retask file get file_abc123

Output fields: file_id, project_id, file_name, mime_type, bytes, storage_path, created_at, updated_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			f, err := svc.GetFile(context.Background(), &commonv1.Id{Id: args[0]})
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, f)
		},
	}
}

// ── file delete ───────────────────────────────────────────────────────────

func newDeleteCommand(gf *flags.Global) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a file",
		Long: `Delete a file by ID.

Usage example:
  retask file delete file_abc123

Output fields: status, file_id`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			_, err = svc.DeleteFile(context.Background(), &commonv1.Id{Id: args[0]})
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, map[string]string{"status": "deleted", "file_id": args[0]})
		},
	}
}

// ── file signed-url ────────────────────────────────────────────────────────

func newSignedURLCommand(gf *flags.Global) *cobra.Command {
	var expiresIn string
	cmd := &cobra.Command{
		Use:   "signed-url <id>",
		Short: "Get a signed URL for a file",
		Long: `Generate a signed download URL for a file.

Usage examples:
  retask file signed-url file_abc123
  retask file signed-url file_abc123 --expires-in 1h

Flags:
  --expires-in string  Duration the signed URL is valid (e.g. 15m, 1h, 24h). Uses server default if omitted.

Output fields: file_id, signed_url, expires_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := &filev1.FileSignedUrlRequest{
				FileId: args[0],
			}

			if cmd.Flags().Changed("expires-in") {
				d, err := time.ParseDuration(expiresIn)
				if err != nil {
					return fmt.Errorf("invalid --expires-in %q: %w", expiresIn, err)
				}
				req.ExpiresIn = durationpb.New(d)
			}

			svc, close, err := connect(gf)
			if err != nil {
				return err
			}
			defer close()
			resp, err := svc.GetFileSignedUrl(context.Background(), req)
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp)
		},
	}
	cmd.Flags().StringVar(&expiresIn, "expires-in", "", "Duration the signed URL is valid (e.g. 15m, 1h, 24h)")
	return cmd
}

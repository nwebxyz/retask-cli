// internal/cmd/file/command.go
package file

import (
	"context"
	"fmt"
	"time"

	connectrpc "connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"
	"github.com/nwebxyz/retask-cli/internal/auth"
	"github.com/nwebxyz/retask-cli/internal/client"
	"github.com/nwebxyz/retask-cli/internal/config"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/output"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
	filev1 "github.com/nwebxyz/retask-cli/proto-gen/file/v1"
	filev1connect "github.com/nwebxyz/retask-cli/proto-gen/file/v1/filev1connect"
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
func connect(gf *flags.Global) (filev1connect.FileServiceClient, func(), error) {
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
	httpClient := client.New(jwt, gf.Insecure, gf.Verbose)
	baseURL := client.BaseURL(profile.Endpoint, gf.Insecure)
	return filev1connect.NewFileServiceClient(httpClient, baseURL, client.Options(gf.Transport)...), func() {}, nil
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

			resp, err := svc.GetFiles(context.Background(), connectrpc.NewRequest(req))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg.Files)
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
			resp, err := svc.GetFile(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
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
			_, err = svc.DeleteFile(context.Background(), connectrpc.NewRequest(&commonv1.Id{Id: args[0]}))
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
			resp, err := svc.GetFileSignedUrl(context.Background(), connectrpc.NewRequest(req))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
	cmd.Flags().StringVar(&expiresIn, "expires-in", "", "Duration the signed URL is valid (e.g. 15m, 1h, 24h)")
	return cmd
}

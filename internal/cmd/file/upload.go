// internal/cmd/file/upload.go
package file

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	connectrpc "connectrpc.com/connect"
	"github.com/spf13/cobra"

	"github.com/nwebxyz/retask-cli/internal/auth"
	"github.com/nwebxyz/retask-cli/internal/client"
	"github.com/nwebxyz/retask-cli/internal/config"
	"github.com/nwebxyz/retask-cli/internal/flags"
	"github.com/nwebxyz/retask-cli/internal/output"
	commentv1 "github.com/nwebxyz/retask-cli/proto-gen/comment/v1"
	commentv1connect "github.com/nwebxyz/retask-cli/proto-gen/comment/v1/commentv1connect"
	commonv1 "github.com/nwebxyz/retask-cli/proto-gen/common/v1"
	filev1connect "github.com/nwebxyz/retask-cli/proto-gen/file/v1/filev1connect"
	taskv1 "github.com/nwebxyz/retask-cli/proto-gen/retask/task/v1"
	taskv1connect "github.com/nwebxyz/retask-cli/proto-gen/retask/task/v1/taskv1connect"
)

// maxUploadBytes mirrors the file service's UploadFileSizeLimitInMB (100 MB).
// Checked client-side because the server surfaces an over-limit body as an
// opaque 500 "Failed to parse multipart form".
const maxUploadBytes = 100 << 20

// uploadResponse is the upload endpoint's success body.
type uploadResponse struct {
	ID string `json:"id"`
}

// errorResponse is the upload endpoint's error body.
type errorResponse struct {
	Error string `json:"error"`
}

// partMimeType returns the MIME type to declare on the multipart part, derived
// from the file extension. An empty return means "unknown": the caller omits the
// Content-Type header entirely so the server falls back to sniffing the bytes.
//
// This mirrors the browser, which sets File.type from the extension. It matters
// because the server prefers the declared type over sniffing, and sniffing
// mislabels ZIP-based containers (.docx, .3mf) as application/zip.
func partMimeType(path string) (mimeType string) {
	ext := filepath.Ext(path)
	if ext == "" {
		return ""
	}
	return strings.TrimSpace(mime.TypeByExtension(ext))
}

// uploadFile POSTs path to the REST upload endpoint and returns the new file ID.
//
// An empty workspaceID selects user-file scope: neither workspace_id nor
// target_nrn is sent, and the server targets the file at the caller's own NRN.
// A non-empty workspaceID requires a targetNrn — the server rejects the pair
// otherwise.
func uploadFile(ctx context.Context, httpClient *http.Client, restEndpoint, path, workspaceID, targetNrn string) (fileID string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if info.Size() > maxUploadBytes {
		return "", fmt.Errorf("%s is %d bytes, over the %d byte upload limit", path, info.Size(), maxUploadBytes)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	// Buffered rather than streamed via io.Pipe: it yields a known
	// Content-Length and avoids chunked transfer-encoding through the gateway.
	// Bounded by maxUploadBytes.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	name := filepath.Base(path)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	// Built by hand rather than via CreateFormFile, which would hardcode
	// application/octet-stream. Omitting the header lets the server sniff.
	if mt := partMimeType(path); mt != "" {
		h.Set("Content-Type", mt)
	}
	part, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	q := url.Values{}
	q.Set("require_ocr", "false")
	if workspaceID != "" {
		q.Set("workspace_id", workspaceID)
		q.Set("target_nrn", targetNrn)
	}

	// The trailing slash is required: the server registers /v1/upload-file/ as a
	// prefix pattern.
	endpoint := strings.TrimSuffix(restEndpoint, "/") + "/v1/upload-file/?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return "", err
	}
	// Authorization is injected by the client's transport.
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload %s: %w", name, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", uploadError(resp.StatusCode, respBody)
	}

	var ur uploadResponse
	if err := json.Unmarshal(respBody, &ur); err != nil || ur.ID == "" {
		return "", fmt.Errorf("upload %s: unexpected response: %s", name, strings.TrimSpace(string(respBody)))
	}
	return ur.ID, nil
}

// uploadError turns a non-2xx upload response into an error, preferring the
// server's {"error": "..."} message. The response Content-Type is not consulted:
// the server writes its status before setting the header, so error bodies ship
// without the JSON content-type.
func uploadError(status int, body []byte) error {
	var er errorResponse
	if err := json.Unmarshal(body, &er); err == nil && er.Error != "" {
		return fmt.Errorf("upload failed (%d): %s", status, er.Error)
	}
	return fmt.Errorf("upload failed with status %d", status)
}

// uploadDeps carries what an upload needs: an authenticated HTTP client, the
// REST endpoint for the bytes, and the gRPC base URL for the follow-up calls.
type uploadDeps struct {
	httpClient   *http.Client
	restEndpoint string
	baseURL      string
	transport    string
}

// resolveUpload loads the profile, resolves the JWT, and returns the endpoints
// an upload needs. It mirrors connect() but exposes the raw HTTP client and the
// REST endpoint instead of a FileServiceClient.
func resolveUpload(gf *flags.Global) (deps uploadDeps, err error) {
	path := gf.ConfigPath
	if path == "" {
		path = config.DefaultConfigPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return uploadDeps{}, err
	}
	profile := cfg.ActiveProfileData(gf.Profile)
	resolver := auth.NewResolver(profile, gf.Profile, gf.WorkspaceID, path, gf.NoSave, gf.Insecure)
	jwt, err := resolver.Token(context.Background())
	if err != nil {
		return uploadDeps{}, err
	}
	return uploadDeps{
		httpClient: client.New(jwt, gf.Insecure, gf.Verbose),
		// Used verbatim: it is already a full URL, unlike Endpoint. Passing it
		// through client.BaseURL would let --insecure rewrite https:// to http://.
		restEndpoint: profile.RestAPIEndpoint,
		baseURL:      client.BaseURL(profile.Endpoint, gf.Insecure),
		transport:    gf.Transport,
	}, nil
}

// taskNrnString builds a task's target NRN: nweb:retask-task:task:<id>.
func taskNrnString(taskID string) (nrn string) {
	return "nweb:retask-task:task:" + taskID
}

// nrnString renders an NRN in its canonical colon-separated string form.
func nrnString(n *commonv1.Nrn) (s string) {
	if n == nil {
		return ""
	}
	return fmt.Sprintf("%s:%s:%s:%s", n.GetDomain(), n.GetService(), n.GetResourceType(), n.GetResourceId())
}

func newUploadCommand(gf *flags.Global) *cobra.Command {
	var taskID, commentID string
	cmd := &cobra.Command{
		Use:   "upload <path>",
		Short: "Upload a file",
		Long: `Upload a local file. With no flags the file is personal: it belongs to you and is
attached to nothing. Pass --task or --comment to attach it in the same step.

Usage examples:
  retask file upload ./report.pdf
  retask file upload ./report.pdf --task task_abc123
  retask file upload ./screenshot.png --comment comment_abc123

Flags:
  --task string     Attach the file to this task
  --comment string  Attach the file to this comment

Output fields: file_id, workspace_id, type, target_nrn, file_name, mime_type, bytes, storage_path, preview_url, download_url, created_by_nrn, created_at`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			ctx := context.Background()

			// An explicitly empty --task/--comment would otherwise fall through to a
			// personal upload, silently uploading somewhere the caller did not mean.
			// Catches the common scripting slip of passing an unset variable.
			if cmd.Flags().Changed("task") && taskID == "" {
				return fmt.Errorf("--task requires a task ID")
			}
			if cmd.Flags().Changed("comment") && commentID == "" {
				return fmt.Errorf("--comment requires a comment ID")
			}

			// A workspace upload must carry a target; the server rejects the pair
			// otherwise. Validate before resolving credentials so the error is fast
			// and offline.
			if (taskID != "" || commentID != "") && gf.WorkspaceID == "" {
				return fmt.Errorf("--task and --comment require a workspace ID (--workspace-id or NWEB_WORKSPACE_ID)")
			}

			deps, err := resolveUpload(gf)
			if err != nil {
				return err
			}

			// Resolve the upload scope.
			var workspaceID, targetNrn string
			switch {
			case taskID != "":
				workspaceID, targetNrn = gf.WorkspaceID, taskNrnString(taskID)
			case commentID != "":
				// A comment NRN is not a legal file target, so the upload targets the
				// comment's parent task. GetComment supplies both that NRN and the
				// authoritative workspace.
				cc := commentv1connect.NewCommentServiceClient(deps.httpClient, deps.baseURL, client.Options(deps.transport)...)
				resp, gerr := cc.GetComment(ctx, connectrpc.NewRequest(&commonv1.Id{Id: commentID}))
				if gerr != nil {
					return gerr
				}
				targetNrn = nrnString(resp.Msg.GetTargetNrn())
				if targetNrn == "" {
					return fmt.Errorf("comment %s has no target task", commentID)
				}
				workspaceID = resp.Msg.GetWorkspaceId()
			}
			// Personal upload: both stay empty, which selects user-file scope.

			fileID, err := uploadFile(ctx, deps.httpClient, deps.restEndpoint, path, workspaceID, targetNrn)
			if err != nil {
				return err
			}

			// Link. The bytes are already stored; a failure here leaves an orphan
			// file, which the message points at so it can be cleaned up.
			switch {
			case taskID != "":
				tc := taskv1connect.NewTaskServiceClient(deps.httpClient, deps.baseURL, client.Options(deps.transport)...)
				if _, aerr := tc.AddTaskAttachments(ctx, connectrpc.NewRequest(&taskv1.AddTaskAttachmentsRequest{
					TaskId:  taskID,
					FileIds: []string{fileID},
				})); aerr != nil {
					return fmt.Errorf("uploaded file %s but failed to attach it to task %s: %w", fileID, taskID, aerr)
				}
			case commentID != "":
				cc := commentv1connect.NewCommentServiceClient(deps.httpClient, deps.baseURL, client.Options(deps.transport)...)
				if _, aerr := cc.AddCommentAttachments(ctx, connectrpc.NewRequest(&commentv1.AddCommentAttachmentsRequest{
					CommentId: commentID,
					FileIds:   []string{fileID},
				})); aerr != nil {
					return fmt.Errorf("uploaded file %s but failed to attach it to comment %s: %w", fileID, commentID, aerr)
				}
			}

			// Read back so every mode prints the same shape, with server-computed
			// mime_type, storage_path and URLs.
			fc := filev1connect.NewFileServiceClient(deps.httpClient, deps.baseURL, client.Options(deps.transport)...)
			resp, err := fc.GetFile(ctx, connectrpc.NewRequest(&commonv1.Id{Id: fileID}))
			if err != nil {
				return err
			}
			return output.Print(gf.Pretty, resp.Msg)
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "Attach the file to this task")
	cmd.Flags().StringVar(&commentID, "comment", "", "Attach the file to this comment")
	cmd.MarkFlagsMutuallyExclusive("task", "comment")
	return cmd
}

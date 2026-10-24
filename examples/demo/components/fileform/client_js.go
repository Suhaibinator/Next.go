//go:build js && wasm

package fileform

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/examples/demo/mutations"
	"github.com/Suhaibinator/Next.go/examples/demo/uploads"
	"github.com/Suhaibinator/Next.go/mutation"
	"reflect"
	"slices"
	"strings"
	"time"
)

type draft struct {
	Title   string
	Files   []client.File
	Receipt uploads.Receipt
}

func clone(d draft) draft {
	d.Files = slices.Clone(d.Files)
	d.Receipt = uploads.CloneReceipt(d.Receipt)
	return d
}
func validate(d draft) client.FieldErrors {
	fields := client.FieldErrors{}
	if !uploads.ValidTitle(d.Title) {
		fields["title"] = "Enter a label from 1 to 80 bytes."
	}
	if len(d.Files) < 1 || len(d.Files) > uploads.MaxFiles {
		fields["files"] = "Choose one or two files."
	}
	for _, file := range d.Files {
		info := file.Info()
		if !uploads.ValidName(info.Name) || info.Size > uploads.MaxFileBytes {
			fields["files"] = "Use valid file names and at most 256 KiB per file."
		}
	}
	return fields
}
func readInput(ctx context.Context, d draft) (uploads.Input, error) {
	input := uploads.Input{Title: d.Title, Files: []uploads.Content{}}
	for _, file := range d.Files {
		data, err := file.Bytes(ctx, uploads.MaxFileBytes)
		if err != nil {
			return uploads.Input{}, &client.FormError{Message: "Could not read the selected files.", Fields: client.FieldErrors{"files": "Select readable files up to 256 KiB each."}}
		}
		input.Files = append(input.Files, uploads.Content{Name: file.Info().Name, Data: data})
	}
	return input, nil
}
func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	model, err := client.NewScopedForm(scope, draft{Title: p.Title, Receipt: p.Receipt}, client.FormOptions[draft]{Clone: clone, Equal: func(a, b draft) bool {
		return a.Title == b.Title && client.SameFiles(a.Files, b.Files) && reflect.DeepEqual(a.Receipt, b.Receipt)
	}, Validate: func(d draft) (client.FieldErrors, error) { return validate(d), nil }, Merge: func(current, submitted, confirmed draft) draft {
		if current.Title != submitted.Title {
			confirmed.Title = current.Title
		}
		if !client.SameFiles(current.Files, submitted.Files) {
			confirmed.Files = slices.Clone(current.Files)
		}
		return confirmed
	}})
	if err != nil {
		return err
	}
	title, err := scope.Require("[data-file-title]")
	if err != nil {
		return err
	}
	titleError, err := scope.Require(`[data-file-error="title"]`)
	if err != nil {
		return err
	}
	if err := client.BindText(scope, title, model, "title", func(d draft) string { return d.Title }, func(d *draft, v string) { d.Title = v }, client.FieldBindingOptions{Error: titleError, ValidateOnBlur: true}); err != nil {
		return err
	}
	files, err := scope.Require("[data-files]")
	if err != nil {
		return err
	}
	filesError, err := scope.Require(`[data-file-error="files"]`)
	if err != nil {
		return err
	}
	if err := client.BindFiles(scope, files, model, "files", func(d draft) []client.File { return d.Files }, func(d *draft, v []client.File) { d.Files = v }, client.FieldBindingOptions{Error: filesError, ValidateOnBlur: true}); err != nil {
		return err
	}
	root, err := scope.Require("[data-file-form]")
	if err != nil {
		return err
	}
	status, err := scope.Require("[data-file-status]")
	if err != nil {
		return err
	}
	receive := func(ctx context.Context, d draft) (client.FormResult[draft], error) {
		var receipt uploads.Receipt
		var err error
		message := "Files received."
		if p.Local || p.JSON {
			input, readErr := readInput(ctx, d)
			if readErr != nil {
				return client.FormResult[draft]{}, readErr
			}
			if p.Local {
				receipt, err = uploads.Compute(ctx, input)
				message = "Local files read."
			} else {
				response, callErr := mutations.UploadFiles.Call(ctx, input, mutation.CallOptions{MaxInputBytes: 1 << 20})
				receipt, err = response.Data, callErr
			}
		} else {
			form, setupErr := client.NewMultipart(1 << 20)
			if setupErr != nil {
				return client.FormResult[draft]{}, setupErr
			}
			defer form.Close()
			if setupErr = form.AddField("title", d.Title); setupErr != nil {
				return client.FormResult[draft]{}, &client.FormError{Message: "Could not prepare the upload."}
			}
			for _, file := range d.Files {
				if setupErr = form.AddFile("files", file); setupErr != nil {
					return client.FormResult[draft]{}, &client.FormError{Message: "Could not prepare the selected files."}
				}
			}
			response, callErr := mutations.UploadFiles.CallMultipart(ctx, form, mutation.CallOptions{MaxInputBytes: 1 << 20})
			receipt, err = response.Data, callErr
		}

		if err != nil {
			var failure *mutation.Error
			if errors.As(err, &failure) && failure != nil && failure.Status == 422 && failure.Code == "validation_error" {
				return client.FormResult[draft]{}, &client.FormError{Message: failure.Message, Fields: client.FieldErrors(failure.Fields)}
			}
			return client.FormResult[draft]{}, err
		}
		confirmed := draft{Title: receipt.Title, Receipt: receipt}
		if p.Local {
			confirmed.Files = slices.Clone(d.Files)
		}
		return client.FormResult[draft]{Value: confirmed, Message: message}, nil
	}
	if !p.Local {
		if err := client.BindSubmit(scope, root, model, receive, client.SubmitOptions{Status: status}); err != nil {
			return err
		}
	} else {
		apply, err := scope.Require("[data-file-apply]")
		if err != nil {
			return err
		}
		reset, err := scope.Require("[data-file-reset]")
		if err != nil {
			return err
		}
		scope.On(apply, "click", func(client.Event) {
			go func() {
				if err := model.Capture(); err != nil {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				ticket, err := model.Begin(ctx)
				if err != nil {
					return
				}
				input, err := ticket.Input()
				if err != nil {
					return
				}
				result, err := receive(ticket.Context(), input)
				if ticket.Context().Err() != nil {
					_ = ticket.Cancel()
					return
				}
				if err != nil {
					var invalid *client.FormError
					if errors.As(err, &invalid) {
						_ = ticket.Reject(invalid.Message, invalid.Fields)
					} else {
						_ = ticket.Fail("Could not read local files.")
					}
					return
				}
				_ = ticket.Accept(result.Value, result.Message)
			}()
		})
		scope.On(reset, "click", func(client.Event) { _ = model.Reset() })
		apply.Property("disabled", false)
		reset.Property("disabled", false)
	}
	cancel, err := scope.Require("[data-file-cancel]")
	if err != nil {
		return err
	}
	scope.On(cancel, "click", func(client.Event) { model.Cancel() })
	selection, err := scope.Require("[data-file-selection]")
	if err != nil {
		return err
	}
	receipt, err := scope.Require("[data-file-receipt]")
	if err != nil {
		return err
	}
	stop, err := model.Observe(func(state client.FormSnapshot[draft]) {
		names := []string{}
		for _, file := range state.Values.Files {
			names = append(names, file.Info().Name)
		}
		text := "No files selected."
		if len(names) > 0 {
			text = strings.Join(names, ", ")
		}
		selection.Text(text)
		receipt.Text(uploads.Summary(state.Values.Receipt))
		cancel.Property("disabled", !state.Pending)
		if p.Local {
			message := "Ready."
			if state.Dirty {
				message = "Unconfirmed local selections."
			}
			if state.Pending {
				message = "Reading selected files…"
			}
			if state.Phase == client.FormSucceeded {
				message = state.Message
			}
			if state.Phase == client.FormFailed {
				message = state.Message
				if message == "" {
					message = "Review the highlighted fields."
				}
			}
			if state.Phase == client.FormCanceled {
				message = "Canceled local file work."
			}
			status.Text(message)
		}
	})
	if err != nil {
		return err
	}
	scope.Cleanup(stop)
	return nil
}

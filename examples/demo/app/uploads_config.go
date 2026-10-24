//go:build !js

package app

import (
	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/examples/demo/mutations"
	"github.com/Suhaibinator/Next.go/examples/demo/uploads"
	"github.com/Suhaibinator/Next.go/mutation"
	"io"
	"net/url"
)

func configureUploads(router *actions.Router) {
	mutations.UploadFiles.Bind(router, func(c *actions.Context, input uploads.Input) (mutation.Result[uploads.Receipt], error) {
		if fields := uploads.Validate(input); len(fields) > 0 {
			return mutation.Result[uploads.Receipt]{}, &actions.ValidationError{Message: "Review the uploaded files.", Fields: fields}
		}
		receipt, err := uploads.Receive(c.Context, input)
		if err != nil {
			return mutation.Result[uploads.Receipt]{}, err
		}
		return mutation.Result[uploads.Receipt]{Data: receipt, Redirect: "/uploads?receipt=" + receipt.ID}, nil
	}, mutation.BindOptions[uploads.Input]{Form: func(c *actions.Context, values url.Values) (uploads.Input, error) {
		invalid := func() (uploads.Input, error) {
			return uploads.Input{}, &actions.ValidationError{Message: "Unexpected upload fields or too many files.", Fields: map[string]string{"files": "Choose one or two files, up to 256 KiB each."}}
		}
		for name, items := range values {
			if name == "files" && len(items) == 1 && items[0] == "" {
				continue
			}
			if (name != "title" && name != "intent") || len(items) != 1 {
				return invalid()
			}
		}
		if intent := values.Get("intent"); intent != "" && intent != "receive" {
			return invalid()
		}
		input := uploads.Input{Title: values.Get("title"), Files: []uploads.Content{}}
		if c.Request.MultipartForm == nil {
			return input, nil
		}
		for name, files := range c.Request.MultipartForm.File {
			if name != "files" || len(files) > uploads.MaxFiles {
				return invalid()
			}
		}
		headers := c.Request.MultipartForm.File["files"]
		if len(headers) > 0 && len(values["files"]) > 0 {
			return invalid()
		}
		for _, header := range headers {
			if err := c.Context.Err(); err != nil {
				return uploads.Input{}, err
			}
			if header.Size > uploads.MaxFileBytes {
				return invalid()
			}
			file, err := header.Open()
			if err != nil {
				return uploads.Input{}, err
			}
			data, readErr := io.ReadAll(io.LimitReader(file, uploads.MaxFileBytes+1))
			closeErr := file.Close()
			if readErr != nil {
				return uploads.Input{}, readErr
			}
			if closeErr != nil {
				return uploads.Input{}, closeErr
			}
			if len(data) > uploads.MaxFileBytes {
				return invalid()
			}
			input.Files = append(input.Files, uploads.Content{Name: header.Filename, Data: data})
		}
		return input, nil
	}})
}

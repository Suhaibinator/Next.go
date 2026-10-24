package mutations

import (
	"github.com/Suhaibinator/Next.go/examples/demo/uploads"
	"github.com/Suhaibinator/Next.go/mutation"
)

var UploadFiles = mutation.Must[uploads.Input, uploads.Receipt]("files/receive", "v1")

package validationform

import "github.com/Suhaibinator/Next.go/examples/form-validation/model"

type Props struct {
	Instance            string
	Values              model.Values
	Action, ValidateURL string
}

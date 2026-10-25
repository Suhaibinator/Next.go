package wirejson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func Strict(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}

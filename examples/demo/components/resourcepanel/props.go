package resourcepanel

import "github.com/Suhaibinator/Next.go/client"

type Data struct {
	Message   string `json:"message"`
	Generated string `json:"generated"`
}
type Props struct {
	Endpoint string
	Initial  Data
	Seed     client.ResourceSeed
}

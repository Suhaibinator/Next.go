//go:build !js

package app

import nextgo "github.com/Suhaibinator/Next.go"

func Page(c *nextgo.Context) (nextgo.Page, error) { return ProductPage(c, "shared") }

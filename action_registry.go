package nextgo

import (
	"fmt"
	"net/http"

	"github.com/Suhaibinator/Next.go/actions"
)

const maxActionRegistrars = 256

func (a *App) initActionRegistrars() (err error) {
	if len(a.config.ActionRegistrars) == 0 {
		return nil
	}
	if len(a.config.ActionRegistrars) > maxActionRegistrars {
		return fmt.Errorf("nextgo: at most %d action registrars are supported", maxActionRegistrars)
	}
	registrars := append([]func(*actions.Router){}, a.config.ActionRegistrars...)
	for index, register := range registrars {
		if register == nil {
			return fmt.Errorf("nextgo: action registrar %d is nil", index)
		}
	}
	var router *actions.Router
	if a.config.Actions == nil {
		router = actions.New(actions.Options{
			// These are the established process-local invalidation methods. Shared
			// stores require explicit fallible ExpireCache calls in the handler.
			OnRevalidate: func(tags, paths []string) {
				for _, tag := range tags {
					a.RevalidateTag(tag)
				}
				for _, path := range paths {
					a.RevalidatePath(path)
				}
			},
			LogError: func(_ *http.Request, cause error) { a.config.Logger.Printf("nextgo action: %v", cause) },
		})
	} else {
		original, ok := a.config.Actions.(*actions.Router)
		if !ok || original == nil {
			return fmt.Errorf("nextgo: action registrars require Config.Actions to be nil or a non-nil *actions.Router")
		}
		router = original.Clone()
	}
	// The clone is unpublished until every registrar succeeds. A bad signature
	// fails Go compilation; invalid/duplicate registrations and application
	// registrar panics fail startup without changing the caller's router.
	index := 0
	completed := false
	defer func() {
		if !completed {
			cause := recover()
			err = fmt.Errorf("nextgo: action registrar %d failed: %v", index, cause)
		}
	}()
	for index = range registrars {
		registrars[index](router)
	}
	a.config.Actions = router
	// Startup factories may capture temporary state that handlers do not need.
	a.config.ActionRegistrars = nil
	completed = true
	return nil
}

package shared

import "context"

func (f *File) ReserveSeeds(ctx context.Context, namespace string) (ticket SeedTicket, err error) {
	err = f.mutate(ctx, func(m *Memory) error { var e error; ticket, e = m.ReserveSeeds(ctx, namespace); return e })
	if err != nil {
		return SeedTicket{}, err
	}
	return
}
func (f *File) BeginSeeds(ctx context.Context, namespace, token string) (session SeedSession, err error) {
	err = f.mutate(ctx, func(m *Memory) error { var e error; session, e = m.BeginSeeds(ctx, namespace, token); return e })
	if err != nil {
		return SeedSession{}, err
	}
	return
}
func (f *File) InstallSeeds(ctx context.Context, namespace, token string, values []SeedValue) (n int, err error) {
	err = f.mutate(ctx, func(m *Memory) error { var e error; n, e = m.InstallSeeds(ctx, namespace, token, values); return e })
	if err != nil {
		return 0, err
	}
	return
}
func (f *File) FinishSeeds(ctx context.Context, namespace, token string) error {
	return f.mutate(ctx, func(m *Memory) error { return m.FinishSeeds(ctx, namespace, token) })
}

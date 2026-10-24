package client

import "net/url"

const historyRecordLimit = 128
const historyURLLimit = 4096

type historyRecord struct{ URL, Previous string }
type historyRegistry struct {
	records map[string]historyRecord
	order   []string
}

func (r *historyRegistry) put(key string, record historyRecord) bool {
	if key == "" || len(record.URL) > historyURLLimit || len(record.Previous) > historyURLLimit {
		return false
	}
	if r.records == nil {
		r.records = map[string]historyRecord{}
	}
	if _, exists := r.records[key]; !exists {
		r.order = append(r.order, key)
		if len(r.order) > historyRecordLimit {
			delete(r.records, r.order[0])
			r.order = r.order[1:]
		}
	}
	r.records[key] = record
	return true
}
func (r *historyRegistry) get(key, current string) (historyRecord, bool) {
	record, ok := r.records[key]
	if !ok || record.URL != current {
		return historyRecord{}, false
	}
	return record, true
}
func returnTargetMatches(previous, target string) bool {
	a, err := url.Parse(previous)
	if err != nil || !a.IsAbs() {
		return false
	}
	b, err := url.Parse(target)
	if err != nil || !b.IsAbs() {
		return false
	}
	if b.Fragment == "" {
		a.Fragment = ""
		a.RawFragment = ""
	}
	return a.String() == b.String()
}

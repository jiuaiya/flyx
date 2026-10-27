package repo

import (
	"sync"
	"testing"

	"go-backend/internal/store/model"
)

func TestSubscriptionCountersIgnoreStaleAndDuplicateSamples(t *testing.T) {
	r, err := Open(t.TempDir() + "/traffic.db")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s := &model.Subscription{Name: "test", Kind: model.SubscriptionShare, Token: randomToken(), Enabled: 1, CreatedTime: 1, UpdatedTime: 1}
	if err := r.SaveSubscription(s); err != nil {
		t.Fatal(err)
	}
	apply := func(epoch string, at int64, up, down int64) {
		t.Helper()
		if err := r.ApplySubscriptionCounterSamples(2, epoch, []SubscriptionCounterSample{
			{SubscriptionID: s.ID, Direction: "up", Bytes: up},
			{SubscriptionID: s.ID, Direction: "up", Bytes: up},
			{SubscriptionID: s.ID, Direction: "down", Bytes: down},
			{SubscriptionID: 99999, Direction: "up", Bytes: 99999},
		}, at); err != nil {
			t.Fatal(err)
		}
	}
	check := func(up, down int64) {
		t.Helper()
		got, err := r.GetSubscription(s.ID)
		if err != nil || got.UploadBytes != up || got.DownloadBytes != down {
			t.Fatalf("got %+v err=%v; want up=%d down=%d", got, err, up, down)
		}
	}
	apply("e1", 100, 100, 300)
	apply("e1", 101, 80, 290)
	apply("e1", 102, 150, 350)
	check(150, 350)
	apply("e2", 200, 10, 20)
	apply("e1", 199, 99999, 99999) // old response cannot restore a retired epoch
	check(160, 370)
	hours, err := r.ListSubscriptionTraffic(s.ID, 0)
	if err != nil || len(hours) != 1 || hours[0].UploadBytes != 160 || hours[0].DownloadBytes != 370 {
		t.Fatalf("history duplicated: %+v %v", hours, err)
	}
	if err := r.ResetSubscriptionTraffic(s.ID, 300); err != nil {
		t.Fatal(err)
	}
	apply("e2", 301, 15, 25)
	check(5, 5) // quota reset keeps the cumulative baseline
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- r.ApplySubscriptionCounterSamples(2, "e3", []SubscriptionCounterSample{{SubscriptionID: s.ID, Direction: "up", Bytes: 100}}, 400)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	check(105, 5)
	var orphan int64
	if err := r.db.Model(&model.SubscriptionCounter{}).Where("subscription_id = ?", 99999).Count(&orphan).Error; err != nil || orphan != 0 {
		t.Fatalf("unknown subscription got counters: %d %v", orphan, err)
	}
}

package budget

import "time"

// testTime is a fixed UTC instant, so dates do not depend on the clock.
var testTime = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

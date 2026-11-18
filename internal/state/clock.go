package state

/*
#if defined(__APPLE__)
#include <mach/mach_time.h>
static double task_monotonic(void) {
 mach_timebase_info_data_t scale;
 mach_timebase_info(&scale);
 return (double)mach_absolute_time() * scale.numer / scale.denom / 1e9;
}
#else
#include <time.h>
static double task_monotonic(void) {
 struct timespec value;
 if (clock_gettime(CLOCK_MONOTONIC, &value) < 0) return -1;
 return (double)value.tv_sec + (double)value.tv_nsec / 1e9;
}
#endif
*/
import "C"

// Monotonic shares the boot-relative origin used by legacy Python activity
// records, rather than Go's process-relative time.Since origin.
func Monotonic() float64 { return float64(C.task_monotonic()) }

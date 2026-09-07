// clock.rs - a guest that asks the host for a clock.
//
// It exists to pin a property of the runtime rather than of the other
// fixtures: those import only the four host functions because their sources
// were written that way, which says nothing about what a hostile guest can
// reach for. This one reaches for wasi_snapshot_preview1.clock_time_get, the
// standard nanosecond timer, and must be refused at instantiation.
//
// Why a clock specifically. Every other sandbox limit in this package is about
// what a guest can DO. A clock is about what a guest can MEASURE: with a
// nanosecond timer inside the same process as the node's signing keys and
// ledger, a guest can time its own host calls and turn any data-dependent
// branch in the host into a side channel. Denying the timer is what makes that
// class of attack unavailable rather than merely difficult.
//
// The risk this guards is a one-line regression: wasi.MustInstantiate(ctx, r)
// is the usual way to make a wasm guest "just work", and it would hand every
// guest a clock at once. No other test in this package would notice, because
// no other fixture asks for one.

#![no_std]
#![no_main]

#[panic_handler]
fn panic(_: &core::panic::PanicInfo) -> ! {
    loop {}
}

#[link(wasm_import_module = "wasi_snapshot_preview1")]
extern "C" {
    fn clock_time_get(clock_id: u32, precision: u64, out: u32) -> u32;
}

static mut SCRATCH: [u8; 8] = [0; 8];

// Returned rather than discarded so the optimizer cannot drop the call and
// with it the import that is the whole point of this fixture.
#[no_mangle]
pub extern "C" fn now() -> u32 {
    unsafe { clock_time_get(1, 0, SCRATCH.as_ptr() as u32) }
}

#[no_mangle]
pub extern "C" fn _start() {
    unsafe {
        core::ptr::write_volatile(SCRATCH.as_mut_ptr(), now() as u8);
    }
}

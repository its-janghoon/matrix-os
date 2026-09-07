// Test fixture for internal/agent: a guest that never returns. The runtime has
// to stop it, or one bad module takes the node down with it.
#![no_std]
#![no_main]

#[panic_handler]
fn panic(_: &core::panic::PanicInfo) -> ! {
    core::arch::wasm32::unreachable()
}

#[no_mangle]
pub extern "C" fn _start() {
    let mut n: u64 = 0;
    loop {
        n = n.wrapping_add(1);
        core::hint::black_box(n);
    }
}

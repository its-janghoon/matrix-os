// Test fixture for internal/agent: a guest that floods the host logger.
//
// It calls log() with a large buffer, in a loop, which is what an attacker does
// to fill a node's disk through a host function that counts LINES and not
// BYTES. no_std, so the only imports are the four host functions.
#![no_std]
#![no_main]

#[panic_handler]
fn panic(_: &core::panic::PanicInfo) -> ! {
    core::arch::wasm32::unreachable()
}

#[link(wasm_import_module = "env")]
unsafe extern "C" {
    fn log(offset: u32, length: u32);
}

// 60 KiB, which fits inside the guest's first memory page and is large enough
// that a per-line cap alone leaves a very large total.
static BIG: [u8; 61440] = [b'A'; 61440];

#[no_mangle]
pub extern "C" fn _start() {
    unsafe {
        let mut i = 0;
        while i < 20000 {
            log((&raw const BIG) as u32, BIG.len() as u32);
            i += 1;
        }
    }
}

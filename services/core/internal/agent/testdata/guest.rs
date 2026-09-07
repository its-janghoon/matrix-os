// Test fixture for internal/agent: a guest module that exercises all four host
// functions and hands the host the offsets it needs to check the results.
//
// no_std so the module imports nothing but the four host functions, which is
// the property the runtime relies on: a guest cannot open a socket or read a
// file because nothing else is in its import table.
#![no_std]
#![no_main]

#[panic_handler]
fn panic(_: &core::panic::PanicInfo) -> ! {
    core::arch::wasm32::unreachable()
}

#[link(wasm_import_module = "env")]
unsafe extern "C" {
    fn log(offset: u32, length: u32);
    fn send(target_offset: u32, target_length: u32, msg_offset: u32, msg_length: u32);
    fn get_memory(offset: u32, length: u32);
    fn set_memory(offset: u32, length: u32);
}

static HELLO: [u8; 15] = *b"hello from wasm";
static TARGET: [u8; 6] = *b"peer-1";
static PAYLOAD: [u8; 4] = *b"ping";
static mut SCRATCH: [u8; 4] = [0; 4];

#[no_mangle]
pub extern "C" fn scratch_ptr() -> u32 {
    (&raw const SCRATCH) as u32
}

#[no_mangle]
pub extern "C" fn _start() {
    unsafe {
        log((&raw const HELLO) as u32, HELLO.len() as u32);
        // Stash PAYLOAD in the host-side buffer, then read it back into SCRATCH.
        // If both host functions work, SCRATCH == PAYLOAD afterwards.
        set_memory((&raw const PAYLOAD) as u32, PAYLOAD.len() as u32);
        get_memory((&raw const SCRATCH) as u32, PAYLOAD.len() as u32);
        send(
            (&raw const TARGET) as u32,
            TARGET.len() as u32,
            (&raw const PAYLOAD) as u32,
            PAYLOAD.len() as u32,
        );
    }
}

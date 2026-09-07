// Test fixture for internal/agent: a guest that tries to read outside its own
// linear memory through the host functions.
//
// The host takes (offset, length) into the GUEST's memory and is supposed to
// refuse a range that is not wholly inside it. This asks for ranges that are
// not: past the end, wrapping the u32, and a length beyond the per-call cap.
#![no_std]
#![no_main]

#[panic_handler]
fn panic(_: &core::panic::PanicInfo) -> ! {
    core::arch::wasm32::unreachable()
}

#[link(wasm_import_module = "env")]
unsafe extern "C" {
    fn log(offset: u32, length: u32);
    fn set_memory(offset: u32, length: u32);
    fn get_memory(offset: u32, length: u32);
    fn send(target_offset: u32, target_length: u32, msg_offset: u32, msg_length: u32);
}

#[no_mangle]
pub extern "C" fn _start() {
    unsafe {
        // Far past the end of any plausible linear memory.
        log(0xFFFF_0000, 1024);
        // A length that wraps offset+length in u32 arithmetic.
        log(0xFFFF_FFF0, 0x20);
        // Over the per-call byte cap.
        log(0, 0xFFFF_FFFF);
        // The same three shapes through the other host functions.
        set_memory(0xFFFF_0000, 1024);
        get_memory(0xFFFF_0000, 1024);
        send(0xFFFF_0000, 16, 0xFFFF_0000, 16);
        // A write target outside the guest's memory.
        get_memory(0xFFFF_FF00, 8);
    }
}

// Test fixture for internal/agent: a guest that calls send() in a loop.
//
// The default posture refuses every send (no policy configured), and refusals
// are still RECORDED so an operator can see what a module tried. This fixture
// is what makes the cost of that recording measurable: a refused send is
// cheaper than a delivered one only if it is not stored.
#![no_std]
#![no_main]

#[panic_handler]
fn panic(_: &core::panic::PanicInfo) -> ! {
    core::arch::wasm32::unreachable()
}

#[link(wasm_import_module = "env")]
unsafe extern "C" {
    fn send(target_offset: u32, target_length: u32, msg_offset: u32, msg_length: u32);
}

static TARGET: [u8; 6] = *b"peer-1";
static BIG: [u8; 61440] = [b'B'; 61440];

#[no_mangle]
pub extern "C" fn _start() {
    unsafe {
        let mut i = 0;
        while i < 20000 {
            send(
                (&raw const TARGET) as u32,
                TARGET.len() as u32,
                (&raw const BIG) as u32,
                BIG.len() as u32,
            );
            i += 1;
        }
    }
}

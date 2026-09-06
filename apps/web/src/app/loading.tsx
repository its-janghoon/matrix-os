export default function Loading() {
  return (
    <div className='min-h-screen bg-black flex items-center justify-center'>
      <div className='text-center'>
        <div className='relative'>
          {/* Animated gradient background */}
          <div className='absolute inset-0 bg-gradient-to-r from-blue-500/20 via-purple-500/20 to-blue-500/20 rounded-xl blur-xl animate-pulse'></div>

          {/* Content */}
          <div className='relative bg-black/50 rounded-xl p-8 border border-blue-500/20'>
            {/* Spinner */}
            <div className='inline-block h-12 w-12 animate-spin rounded-full border-4 border-solid border-blue-400 border-r-transparent motion-reduce:animate-[spin_1.5s_linear_infinite] mb-4'></div>

            {/* Loading text */}
            <h2 className='text-2xl font-bold text-white mt-4'>Loading</h2>
            <p className='text-gray-400 mt-2'>Preparing your documentation...</p>
          </div>
        </div>
      </div>
    </div>
  );
}

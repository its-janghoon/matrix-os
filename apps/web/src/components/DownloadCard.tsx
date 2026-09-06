import { FC } from 'react';
import { IconType } from 'react-icons';
import { FiDownload, FiCopy } from 'react-icons/fi';

interface DownloadCardProps {
  platform: string;
  version: string;
  architecture: string;
  fileSize: string;
  sha256: string;
  downloadUrl: string;
  icon: IconType;
}

const DownloadCard: FC<DownloadCardProps> = ({
  platform,
  version,
  architecture,
  fileSize,
  sha256,
  downloadUrl,
  icon: Icon
}) => {
  const copyHash = () => {
    navigator.clipboard.writeText(sha256);
  };

  return (
    <div className="bg-gray-900 rounded-lg p-6 hover:bg-gray-800 transition-colors border border-gray-800">
      <div className="flex items-start justify-between mb-4">
        <div className="flex items-center gap-3">
          <Icon className="w-6 h-6 text-blue-500" />
          <div>
            <h3 className="text-xl font-semibold">{platform}</h3>
            <p className="text-gray-400">{architecture}</p>
          </div>
        </div>
        <a
          href={downloadUrl}
          className="flex items-center gap-2 px-4 py-2 bg-blue-600 rounded-lg hover:bg-blue-700 transition-colors"
        >
          <FiDownload className="w-4 h-4" />
          Download
        </a>
      </div>
      <div className="space-y-2">
        <div className="flex items-center justify-between text-sm text-gray-400">
          <span>Version</span>
          <span>{version}</span>
        </div>
        <div className="flex items-center justify-between text-sm text-gray-400">
          <span>File size</span>
          <span>{fileSize}</span>
        </div>
        <div className="flex items-center justify-between text-sm text-gray-400">
          <span>SHA256</span>
          <div className="flex items-center gap-2">
            <span className="font-mono text-xs">{sha256.slice(0, 16)}...{sha256.slice(-16)}</span>
            <button
              onClick={copyHash}
              className="p-1 hover:text-white transition-colors"
              title="Copy full hash"
            >
              <FiCopy className="w-4 h-4" />
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};

export default DownloadCard; 
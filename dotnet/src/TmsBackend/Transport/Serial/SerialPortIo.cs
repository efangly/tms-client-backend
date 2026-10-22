using System.IO.Ports;

namespace TmsBackend.Transport.Serial;

/// <summary>Configuration for one persistent serial port (serialclient.PortConfig).</summary>
public sealed record SerialPortConfig
{
    /// <summary>e.g. "COM3".</summary>
    public required string Path { get; init; }
    public int BaudRate { get; init; } = 9600;
    public bool AutoReconnect { get; init; }
    public TimeSpan ReconnectInterval { get; init; } = TimeSpan.FromSeconds(5);
    /// <summary>
    /// Wait after opening before the port is usable. USB-serial adapters
    /// (e.g. FTDI FT232) reset the MCU when DTR/RTS assert on open and it
    /// needs a moment to boot.
    /// </summary>
    public TimeSpan BootSettleDelay { get; init; } = TimeSpan.FromSeconds(2);
}

/// <summary>The I/O a <see cref="SerialConnection"/> needs from a port; faked in tests.</summary>
public interface ISerialPortIo : IDisposable
{
    void DiscardInBuffer();
    void Write(byte[] data);
    /// <summary>Reads available bytes, waiting at most <paramref name="timeout"/>. Returns 0 on timeout.</summary>
    int Read(byte[] buffer, TimeSpan timeout);
}

/// <summary><see cref="SerialPort"/> opened 8N1 with DTR and RTS asserted, matching go.bug.st/serial's defaults.</summary>
public sealed class SystemSerialPortIo : ISerialPortIo
{
    private readonly SerialPort _port;

    public SystemSerialPortIo(SerialPortConfig config)
    {
        _port = new SerialPort(config.Path, config.BaudRate, Parity.None, 8, StopBits.One)
        {
            DtrEnable = true,
            RtsEnable = true,
            WriteTimeout = 3000,
        };
        _port.Open();
    }

    public void DiscardInBuffer() => _port.DiscardInBuffer();

    public void Write(byte[] data) => _port.Write(data, 0, data.Length);

    public int Read(byte[] buffer, TimeSpan timeout)
    {
        _port.ReadTimeout = Math.Max(1, (int)timeout.TotalMilliseconds);
        try
        {
            return _port.Read(buffer, 0, buffer.Length);
        }
        catch (TimeoutException)
        {
            return 0;
        }
    }

    public void Dispose() => _port.Dispose();
}

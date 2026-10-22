using System.Diagnostics;
using System.Text;
using TmsBackend.Protocol;

namespace TmsBackend.Transport.Serial;

/// <summary>
/// One persistent serial port with auto-reconnect (serialclient.Connection).
/// Unlike TCP, the port stays open between requests.
/// </summary>
public sealed class SerialConnection : IDisposable
{
    private readonly Lock _lock = new();
    // Serializes whole request/response exchanges. Go only locked the state, so
    // the poll loop and the alert loop could interleave commands on one port.
    private readonly SemaphoreSlim _io = new(1, 1);
    private readonly Func<SerialPortConfig, ISerialPortIo> _open;
    private readonly ILogger _logger;
    private ISerialPortIo? _port;
    private bool _connected;
    private bool _stopped;
    private Timer? _reconnectTimer;

    public SerialConnection(SerialPortConfig config, Func<SerialPortConfig, ISerialPortIo> open, ILogger logger)
    {
        Config = config;
        _open = open;
        _logger = logger;
    }

    public SerialPortConfig Config { get; }

    public bool IsConnected
    {
        get { lock (_lock) return _connected; }
    }

    internal bool IsStopped
    {
        get { lock (_lock) return _stopped; }
    }

    internal bool HasPendingReconnect
    {
        get { lock (_lock) return _reconnectTimer != null; }
    }

    /// <summary>
    /// Opens the port; a no-op when already connected. On failure with
    /// AutoReconnect, a retry is scheduled so a device plugged in later still connects.
    /// </summary>
    public void Connect()
    {
        lock (_lock)
        {
            try
            {
                ConnectLocked();
            }
            catch (Exception) when (Config.AutoReconnect && !_stopped)
            {
                ScheduleReconnectLocked();
                throw;
            }
        }
    }

    private void ConnectLocked()
    {
        if (_connected && _port != null)
        {
            _logger.LogInformation("Serial {Path}: already connected, skipping connect", Config.Path);
            return;
        }

        var port = _open(Config);
        _logger.LogInformation("Serial {Path}: opened, waiting {Delay} for device boot...", Config.Path, Config.BootSettleDelay);
        Thread.Sleep(Config.BootSettleDelay);

        _port = port;
        _connected = true;
        _logger.LogInformation("Serial {Path}: connected (baud={Baud})", Config.Path, Config.BaudRate);
    }

    /// <summary>Cancels any pending reconnect and closes the port for good.</summary>
    public void Disconnect()
    {
        lock (_lock)
        {
            _stopped = true;
            CancelReconnectLocked();
            CloseLocked();
        }
    }

    private void CloseLocked()
    {
        if (_port != null)
        {
            try
            {
                _port.Dispose();
            }
            catch (Exception e)
            {
                _logger.LogWarning("Serial {Path}: close error: {Error}", Config.Path, e.Message);
            }
            _port = null;
        }
        _connected = false;
    }

    /// <summary>A failed read/write is the only signal that the port dropped: close it and schedule a reconnect.</summary>
    internal void HandleIOError(Exception e)
    {
        lock (_lock)
        {
            _logger.LogWarning("Serial {Path}: I/O error, marking disconnected: {Error}", Config.Path, e.Message);
            CloseLocked();
            if (Config.AutoReconnect && !_stopped)
                ScheduleReconnectLocked();
        }
    }

    // A single pending timer at most, however many failures happen.
    private void ScheduleReconnectLocked()
    {
        if (_reconnectTimer != null)
            return;
        _reconnectTimer = new Timer(_ => Reconnect(), null, Config.ReconnectInterval, Timeout.InfiniteTimeSpan);
    }

    private void Reconnect()
    {
        lock (_lock)
        {
            _reconnectTimer?.Dispose();
            _reconnectTimer = null;
            if (_stopped)
                return;
            try
            {
                ConnectLocked();
            }
            catch (Exception e)
            {
                _logger.LogWarning("Serial {Path}: reconnect failed: {Error}", Config.Path, e.Message);
                if (Config.AutoReconnect && !_stopped)
                    ScheduleReconnectLocked();
            }
        }
    }

    private void CancelReconnectLocked()
    {
        _reconnectTimer?.Dispose();
        _reconnectTimer = null;
    }

    /// <summary>Sends <paramref name="command"/> (default "A") and parses the reply, retrying anomalous readings.</summary>
    public async Task<ProbeResponse> RequestAsync(string command, TimeSpan timeout, CancellationToken ct = default)
    {
        if (command == "")
            command = "A";

        await _io.WaitAsync(ct);
        try
        {
            return await ProbeResponse.RequestWithRetryAsync(Config.Path,
                c => Task.Run(() => (SendOnce(command, timeout), true), c), _logger, ct);
        }
        finally
        {
            _io.Release();
        }
    }

    /// <summary>Writes command + "\r" and reads one frame ending in 0x0D, or whatever arrived before the timeout.</summary>
    private byte[] SendOnce(string command, TimeSpan timeout)
    {
        ISerialPortIo port;
        lock (_lock)
        {
            if (!_connected || _port == null)
                throw new ProbeTransportException($"serial {Config.Path}: not connected", connected: false);
            port = _port;
        }

        try
        {
            // Drop bytes left over from an earlier timed-out exchange so they
            // can't be glued onto this frame.
            port.DiscardInBuffer();
            port.Write(Encoding.ASCII.GetBytes(command + "\r"));
        }
        catch (Exception e) when (e is not ProbeTransportException)
        {
            HandleIOError(e);
            throw new ProbeTransportException($"serial {Config.Path}: write: {e.Message}", connected: false, e);
        }

        var frame = new List<byte>();
        var buffer = new byte[256];
        var sw = Stopwatch.StartNew();
        while (sw.Elapsed < timeout)
        {
            int n;
            try
            {
                n = port.Read(buffer, timeout - sw.Elapsed);
            }
            catch (Exception e)
            {
                HandleIOError(e);
                throw new ProbeTransportException($"serial {Config.Path}: read: {e.Message}", connected: false, e);
            }
            if (n == 0)
                break; // Read timeout with no further data.
            frame.AddRange(buffer.AsSpan(0, n));
            if (frame[^1] == 0x0D)
                break;
        }
        return frame.ToArray();
    }

    /// <summary>Test hook: puts the connection into the connected state with <paramref name="port"/>.</summary>
    internal void SetConnectedForTest(ISerialPortIo port)
    {
        lock (_lock)
        {
            _port = port;
            _connected = true;
        }
    }

    public void Dispose()
    {
        Disconnect();
        _io.Dispose();
    }
}

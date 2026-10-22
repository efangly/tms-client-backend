using System.Net.Sockets;
using System.Text;
using TmsBackend.Protocol;

namespace TmsBackend.Transport;

/// <summary>
/// Requests readings from a network probe: a fresh TCP connection per request,
/// send command + "\r", read until the 0x0D frame terminator or the timeout
/// (tcpclient.RequestFromTCPServer).
/// </summary>
public sealed class TcpProbeClient(ILogger<TcpProbeClient> logger)
{
    public Task<ProbeResponse> RequestAsync(string ip, int port, string command, TimeSpan timeout, CancellationToken ct = default)
    {
        if (command == "")
            command = "A";
        return ProbeResponse.RequestWithRetryAsync(ip, c => SendOnceAsync(ip, port, command, timeout, c), logger, ct);
    }

    private static async Task<(byte[] Frame, bool Connected)> SendOnceAsync(string ip, int port, string command, TimeSpan timeout, CancellationToken ct)
    {
        using var client = new TcpClient();
        using (var dialCts = CancellationTokenSource.CreateLinkedTokenSource(ct))
        {
            dialCts.CancelAfter(timeout);
            try
            {
                await client.ConnectAsync(ip, port, dialCts.Token);
            }
            catch (Exception e) when (e is SocketException or OperationCanceledException && !ct.IsCancellationRequested)
            {
                var reason = e is OperationCanceledException ? "i/o timeout" : e.Message;
                throw new ProbeTransportException($"Connection failed: dial tcp {ip}:{port}: {reason}", connected: false, e);
            }
        }

        // One deadline for write + read, as conn.SetDeadline in Go.
        using var cts = CancellationTokenSource.CreateLinkedTokenSource(ct);
        cts.CancelAfter(timeout);
        var stream = client.GetStream();

        try
        {
            await stream.WriteAsync(Encoding.ASCII.GetBytes(command + "\r"), cts.Token);
        }
        catch (Exception e) when (e is IOException or SocketException or OperationCanceledException && !ct.IsCancellationRequested)
        {
            throw new ProbeTransportException($"Write failed: {e.Message}", connected: true, e);
        }

        var frame = new List<byte>();
        var buffer = new byte[1024];
        while (true)
        {
            int n;
            try
            {
                n = await stream.ReadAsync(buffer, cts.Token);
            }
            catch (Exception e) when (e is IOException or SocketException or OperationCanceledException && !ct.IsCancellationRequested)
            {
                break; // Timeout or reset ends the frame, like Go's read loop.
            }
            if (n == 0)
                break; // EOF
            frame.AddRange(buffer.AsSpan(0, n));
            if (frame[^1] == 0x0D)
                break;
        }
        return (frame.ToArray(), true);
    }
}

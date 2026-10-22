using System.Text.Json;
using TmsBackend.Config;
using TmsBackend.Json;
using TmsBackend.Protocol;
using TmsBackend.Transport;
using TmsBackend.Transport.Serial;

namespace TmsBackend.Infrastructure;

/// <summary>
/// `tms-backend.exe probe &lt;ip|COMx&gt; [command]`: reads one device once and
/// prints the raw frame and parsed probes. For on-site troubleshooting without
/// starting the server (stop the service first when testing a COM port — ports are exclusive).
/// </summary>
public static class ProbeCommand
{
    public static async Task<int> RunAsync(string[] args, AppOptions options)
    {
        if (args.Length < 2)
        {
            Console.Error.WriteLine("usage: tms-backend probe <ip|COMx> [command]");
            return 2;
        }
        var target = args[1];
        var command = args.Length > 2 ? args[2] : "A";

        using var loggerFactory = LoggerFactory.Create(b => b.AddSimpleConsole(o => o.SingleLine = true));
        ProbeResponse response;
        if (SerialService.IsSerialPath(target))
        {
            using var conn = new SerialConnection(new SerialPortConfig
            {
                Path = target,
                BaudRate = options.DefaultSerialBaud,
                BootSettleDelay = options.DefaultSerialBootDelay,
            }, c => new SystemSerialPortIo(c), loggerFactory.CreateLogger("probe"));
            try
            {
                conn.Connect();
            }
            catch (Exception e)
            {
                Console.Error.WriteLine($"cannot open {target}: {e.Message}");
                return 1;
            }
            response = await conn.RequestAsync(command, TimeSpan.FromSeconds(5));
        }
        else
        {
            var tcp = new TcpProbeClient(loggerFactory.CreateLogger<TcpProbeClient>());
            response = await tcp.RequestAsync(target, options.DefaultTcpPort, command, TimeSpan.FromSeconds(5));
        }

        Console.WriteLine(JsonSerializer.Serialize(new
        {
            source = response.Source,
            connected = response.Connected,
            data = response.Data,
            error = response.Error,
            probes = response.Probes,
        }, new JsonSerializerOptions(GoJson.Options) { WriteIndented = true }));
        return response.Error == "" && response.Probes.Count > 0 ? 0 : 1;
    }
}

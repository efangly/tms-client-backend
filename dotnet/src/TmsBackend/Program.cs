using System.Globalization;
using Microsoft.Extensions.Hosting.WindowsServices;
using Microsoft.Extensions.Logging.EventLog;
using TmsBackend.Config;
using TmsBackend.Data;
using TmsBackend.Endpoints;
using TmsBackend.Json;
using TmsBackend.Infrastructure;
using TmsBackend.Logging;
using TmsBackend.Services;
using TmsBackend.Transport;
using TmsBackend.Transport.Serial;

const string ServiceName = "TMSBackend";

// A th-TH machine would otherwise format years in the Buddhist calendar (2569)
// and use locale date separators; Go always used fixed invariant formats.
CultureInfo.DefaultThreadCurrentCulture = CultureInfo.InvariantCulture;
CultureInfo.DefaultThreadCurrentUICulture = CultureInfo.InvariantCulture;
CultureInfo.CurrentCulture = CultureInfo.InvariantCulture;
CultureInfo.CurrentUICulture = CultureInfo.InvariantCulture;

if (args is ["--version"] or ["-v"])
{
    Console.WriteLine(AppVersion.Value);
    return 0;
}

StartupDiag.Write("[1/4] main() started");

// Remember the launch directory before switching to the exe directory: under
// `dotnet run` the exe lives in bin/, so .env falls back to the launch dir.
var launchDir = Environment.CurrentDirectory;
var exeDir = AppContext.BaseDirectory;
try
{
    // Services start in C:\Windows\System32; relative paths (logs/, archives/) must resolve next to the exe.
    Directory.SetCurrentDirectory(exeDir);
    StartupDiag.Write("[2/4] Working directory set OK");
}
catch (Exception e)
{
    StartupDiag.Write($"[ERROR] could not change to exe directory: {e.Message}");
}

var envPath = EnvFile.Load(exeDir, launchDir);
StartupDiag.Write(envPath != null ? $"[3/4] .env loaded OK ({envPath})" : "[WARN] .env not found");

var configWarnings = new List<string>();
var options = AppOptions.FromEnvironment(configWarnings.Add);

if (args is ["probe", ..])
    return await ProbeCommand.RunAsync(args, options);
if (args is ["archive-table", ..])
    return await ArchiveTableCommand.RunAsync(args, options);

var isService = WindowsServiceHelpers.IsWindowsService();
StartupDiag.Write($"[4/4] Starting {(isService ? "service" : "console")} on port {options.Port}");

var builder = WebApplication.CreateBuilder(new WebApplicationOptions
{
    Args = args,
    ContentRootPath = exeDir,
});

builder.Services.AddWindowsService(o => o.ServiceName = ServiceName);
builder.Services.Configure<HostOptions>(o =>
{
    // Same budget as cleanup() in main.go before it force-exits.
    o.ShutdownTimeout = TimeSpan.FromSeconds(15);
    // A crashing background loop must not stop the whole app (Go: recover()).
    o.BackgroundServiceExceptionBehavior = BackgroundServiceExceptionBehavior.Ignore;
});

builder.Logging.ClearProviders();
builder.Logging.SetMinimumLevel(LogLevel.Information);
builder.Logging.AddFilter("Microsoft.AspNetCore", LogLevel.Warning);
builder.Logging.AddSimpleConsole(o =>
{
    o.SingleLine = true;
    o.TimestampFormat = "yyyy/MM/dd HH:mm:ss ";
});
builder.Logging.AddProvider(new ErrorFileLoggerProvider(Path.Combine(exeDir, "logs")));
if (isService && OperatingSystem.IsWindows())
{
    builder.Logging.AddEventLog(new EventLogSettings { SourceName = ServiceName });
    builder.Logging.AddFilter<EventLogLoggerProvider>(level => level >= LogLevel.Warning);
}

builder.WebHost.ConfigureKestrel(k => k.ListenAnyIP(options.Port));

builder.Services.AddSingleton(options);
builder.Services.AddSingleton<AppReadiness>();
builder.Services.AddSingleton<Db>();
builder.Services.AddSingleton<IMachineRepository, MachineRepository>();
builder.Services.AddSingleton<ITempLogRepository, TempLogRepository>();
builder.Services.AddSingleton<ITempErrorRepository, TempErrorRepository>();
builder.Services.AddSingleton<IArchiveRepository, ArchiveRepository>();
builder.Services.AddSingleton<TcpProbeClient>();
builder.Services.AddSingleton<SerialService>();
builder.Services.AddHostedService(sp => sp.GetRequiredService<SerialService>());
builder.Services.AddSingleton<IProbeRequester, ProbeRequester>();
builder.Services.AddSingleton<IDbPing>(sp => sp.GetRequiredService<Db>());
builder.Services.AddSingleton(TimeProvider.System);
builder.Services.AddSingleton<EventHub>();
builder.Services.AddHttpClient<ILegacyApi, LegacyApiService>(c => c.Timeout = TimeSpan.FromSeconds(10));
builder.Services.AddSingleton<MqttService>();
builder.Services.AddSingleton<IMqttPublisher>(sp => sp.GetRequiredService<MqttService>());
builder.Services.AddHostedService(sp => sp.GetRequiredService<MqttService>());
builder.Services.AddSingleton<PollingService>();
builder.Services.AddHostedService(sp => sp.GetRequiredService<PollingService>());
builder.Services.AddSingleton<ArchiveService>();
builder.Services.AddHostedService(sp => sp.GetRequiredService<ArchiveService>());
// Fiber cors.New() defaults: any origin, common methods.
builder.Services.AddCors(o => o.AddDefaultPolicy(p => p.AllowAnyOrigin().AllowAnyMethod().AllowAnyHeader()));
builder.Services.ConfigureHttpJsonOptions(o => GoJson.Configure(o.SerializerOptions));
builder.Services.AddHostedService<StartupWorker>();

var app = builder.Build();
var logger = app.Services.GetRequiredService<ILoggerFactory>().CreateLogger("TmsBackend");

AppDomain.CurrentDomain.UnhandledException += (_, e) =>
{
    StartupDiag.Write($"[FATAL] unhandled exception: {e.ExceptionObject}");
    logger.LogCritical(e.ExceptionObject as Exception, "Unhandled exception");
};
TaskScheduler.UnobservedTaskException += (_, e) =>
{
    logger.LogError(e.Exception, "Unobserved task exception");
    e.SetObserved();
};

app.UseTmsRequestLogging();
app.UseCors();
app.MapTmsApi();

if (!isService)
{
    try { Console.Title = $"TMS Backend {AppVersion.Value} - port {options.Port}"; }
    catch (IOException) { /* no console window (output redirected) */ }
}

logger.LogInformation("TMS Backend version: {Version}", AppVersion.Value);
logger.LogInformation("Working directory: {Dir}", Environment.CurrentDirectory);
logger.LogInformation("Mode: {Mode}", isService ? "Windows Service" : "Console (press Ctrl+C to stop)");
if (envPath == null)
    logger.LogWarning(".env not found next to the exe or in {LaunchDir}; using environment/defaults", launchDir);
else
    logger.LogInformation("Loaded config from {Path}", envPath);
foreach (var w in configWarnings)
    logger.LogWarning("{Warning}", w);
foreach (var ip in Startup.LocalIPv4())
    logger.LogInformation("Local IP: {Ip}", ip);
logger.LogInformation("========================================");
logger.LogInformation("Starting TMS Backend Server on port {Port}", options.Port);
logger.LogInformation("========================================");

try
{
    await app.RunAsync();
    logger.LogInformation("Shutdown complete");
    return 0;
}
catch (Exception e)
{
    // Typically "address already in use" when PORT is taken.
    logger.LogCritical(e, "Failed to start server on port {Port}", options.Port);
    StartupDiag.Write($"[FATAL] {e.Message}");
    return 1;
}

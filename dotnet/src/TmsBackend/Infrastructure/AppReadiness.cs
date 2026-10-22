namespace TmsBackend.Infrastructure;

/// <summary>
/// Signals when startup dependencies (network, database) are ready. Background
/// services await <see cref="WaitAsync"/> before their first tick, replacing
/// the strict sequential startup in main.go's startServer — the host itself must
/// start immediately so the Windows SCM sees the service as running.
/// </summary>
public sealed class AppReadiness
{
    private readonly TaskCompletionSource _ready = new(TaskCreationOptions.RunContinuationsAsynchronously);

    public bool IsReady => _ready.Task.IsCompletedSuccessfully;

    public void MarkReady() => _ready.TrySetResult();

    public Task WaitAsync(CancellationToken ct) => _ready.Task.WaitAsync(ct);
}

using System.Threading.Channels;

namespace TmsBackend.Services;

/// <summary>
/// Fan-out of polling events to SSE clients (Subscribe/notifySubscribers in
/// polling.go). Each subscriber gets a 10-slot buffer; when it is full the
/// new event is dropped rather than blocking the polling loop.
/// </summary>
public sealed class EventHub
{
    public const int BufferSize = 10;

    private readonly Lock _lock = new();
    private readonly List<Channel<DataSavedEvent>> _saved = [];
    private readonly List<Channel<IReadOnlyList<TemperatureUpdateEvent>>> _temperature = [];

    public Channel<DataSavedEvent> Subscribe() => Add(_saved);

    public void Unsubscribe(Channel<DataSavedEvent> ch) => Remove(_saved, ch);

    public Channel<IReadOnlyList<TemperatureUpdateEvent>> SubscribeTemperature() => Add(_temperature);

    public void UnsubscribeTemperature(Channel<IReadOnlyList<TemperatureUpdateEvent>> ch) => Remove(_temperature, ch);

    public void Publish(DataSavedEvent e) => Broadcast(_saved, e);

    public void Publish(IReadOnlyList<TemperatureUpdateEvent> events) => Broadcast(_temperature, events);

    internal int SubscriberCount
    {
        get { lock (_lock) return _saved.Count + _temperature.Count; }
    }

    private Channel<T> Add<T>(List<Channel<T>> list)
    {
        var ch = Channel.CreateBounded<T>(new BoundedChannelOptions(BufferSize)
        {
            FullMode = BoundedChannelFullMode.DropWrite,
            SingleReader = true,
        });
        lock (_lock)
            list.Add(ch);
        return ch;
    }

    private void Remove<T>(List<Channel<T>> list, Channel<T> ch)
    {
        lock (_lock)
        {
            if (list.Remove(ch))
                ch.Writer.TryComplete();
        }
    }

    private void Broadcast<T>(List<Channel<T>> list, T item)
    {
        lock (_lock)
        {
            foreach (var ch in list)
                ch.Writer.TryWrite(item);
        }
    }
}

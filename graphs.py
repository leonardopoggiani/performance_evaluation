import sqlite3
import matplotlib.pyplot as plt
import matplotlib.dates as mdates

# Connect to the database
conn = sqlite3.connect('./db/checkpoint_sizes.db')
cursor = conn.cursor()

# Query the database for all rows in the checkpoint_sizes table
cursor.execute("SELECT * FROM checkpoint_sizes")
rows = cursor.fetchall()

# Create empty lists to store the timestamps and sizes
timestamps = []
sizes = []

# Extract the timestamps and sizes from the rows
for row in rows:
    timestamps.append(row[0])
    sizes.append(row[2])

# Convert the timestamps to Matplotlib date format
timestamps = [mdates.date2num(t) for t in timestamps]

# Plot the data as a bar graph
fig, ax = plt.subplots()
ax.bar(timestamps, sizes, width=1.0/24)
ax.xaxis_date()
ax.set_xlabel('Timestamp')
ax.set_ylabel('Checkpoint Size (MB)')
ax.set_title('Checkpoint Sizes Over Time')
plt.show()

# Close the database connection
conn.close()


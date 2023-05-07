import sqlite3
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
import os

# Connect to database
conn = sqlite3.connect('./db/checkpoint_sizes.db')
c = conn.cursor()

# Get data from database
c.execute('SELECT timestamp, containers, size FROM checkpoint_sizes')
data = c.fetchall()

# Create dictionary to store data by numContainers
container_data = {}
for container in set([x[1] for x in data]):
    container_data[container] = []

# Populate dictionary
for row in data:
    container_data[row[1]].append((row[0], row[2]))

# Create figure
fig, ax = plt.subplots()

# Plot data for each numContainers
for container in container_data:
    x = [row[0] for row in container_data[container]]
    y = [row[1] for row in container_data[container]]
    ax.plot(x, y, label=f"{container} containers")

# Format graph
ax.set_xlabel('Timestamp')
ax.set_ylabel('Checkpoint size (MB)')
ax.legend()
fig.autofmt_xdate()

# Save graph image to "fig" folder
if not os.path.exists("fig"):
    os.makedirs("fig")
fig.savefig("fig/checkpoint_sizes.png")

# Close database connection
conn.close()
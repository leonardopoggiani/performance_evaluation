import sqlite3
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
import os
import pprint
import datetime


def graph_checkpoint_size(c):
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

  # Calculate averages
  averages = {}
  for container, values in container_data.items():
      sizes = [v[1] for v in values]
      avg_size = sum(sizes) / len(sizes)
      averages[container] = avg_size

  pprint.pprint(container_data)
  pprint.pprint(averages)

  # Create figure
  fig = plt.figure()

  # Plot data for each numContainers
  for container in container_data:
      x = [row[0] for row in container_data[container]]
      y = [row[1] for row in container_data[container]]

      # Plot the sizes as a bar chart
      plt.bar(x, y, color='b', align='center', width=0.2, label=f'{container} container')

  # Plot the averages as a line chart
  x = [int(container) for container in averages.keys()]
  y = list(averages.values())
  plt.plot(x, y, color='r', label='Average')

  plt.xlabel('Number of containers')
  plt.ylabel('Checkpoint size (MB)')
  plt.title('Checkpoint sizes by number of containers')
  plt.legend()
  fig.autofmt_xdate()

  # Save graph image to "fig" folder
  if not os.path.exists("fig"):
      os.makedirs("fig")

  filename = "checkpoint_sizes.png"
  timestamp = datetime.datetime.now().strftime("%Y-%m-%d-%H-%M-%S")
  new_filename = os.path.splitext(filename)[0] + "_" + timestamp + os.path.splitext(filename)[1]

  fig.savefig(f"fig/{new_filename}")

  return


def graph_checkpoint_time(c):
   # Get data from database
  c.execute('SELECT timestamp, containers, elapsed FROM checkpoint_times')
  data = c.fetchall()

  # Create dictionary to store data by numContainers
  container_data = {}
  for container in set([x[1] for x in data]):
      container_data[container] = []

  # Populate dictionary
  for row in data:
      container_data[row[1]].append((row[0], row[2]))

  pprint.pprint(container_data)

  # Create figure
  fig = plt.figure()

  # Plot data for each numContainers
  for container in container_data:
      x = [row[0] for row in container_data[container]]
      y = [row[1] for row in container_data[container]]

      # Plot the sizes as a bar chart
      plt.bar(x, y, color='b', align='center', width=0.2, label=f'{container} container')
    
  plt.xlabel('Number of containers')
  plt.ylabel('Time elapsed (ms)')
  plt.title('Checkpoint times by number of containers')
  plt.legend()
  fig.autofmt_xdate()

  # Save graph image to "fig" folder
  if not os.path.exists("fig"):
      os.makedirs("fig")


  filename = "checkpoint_times.png"
  timestamp = datetime.datetime.now().strftime("%Y-%m-%d-%H-%M-%S")
  new_filename = os.path.splitext(filename)[0] + "_" + timestamp + os.path.splitext(filename)[1]

  fig.savefig(f"fig/{new_filename}")

  return


if __name__ == "__main__":
  print("Graphing data...")

  # Connect to database
  conn = sqlite3.connect('./db/checkpoint_data.db')
  c = conn.cursor()

  graph_checkpoint_size(c)

  graph_checkpoint_time(c)

  # Close database connection
  conn.close()